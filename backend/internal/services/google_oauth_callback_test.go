package services

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"gorm.io/gorm"
)

// Full mocked end-to-end tests for the Google OAuth flow (29 Agu 2026). The
// Google token endpoint is impersonated by an httptest server and id_tokens
// are signed with a throwaway RSA key — NO real Google credentials, NO
// network. Distinctive LEAK-MARKER values let the audit tests prove secrets
// and tokens never reach the log output.
const (
	mockGoogleClientID     = "mock-client-id"
	mockGoogleClientSecret = "mock-client-secret-LEAK-MARKER"
	mockProviderAccessTok  = "ya29.mock-provider-access-token-LEAK-MARKER"
	mockAuthCode           = "mock-authorization-code-LEAK-MARKER"
)

// googleMockEnv wires a fully mocked Google OAuth environment: in-memory repo,
// real AuthService (real JWT), and a GoogleOAuthService whose exchange hits a
// local mock token endpoint.
type googleMockEnv struct {
	repo   *mockOAuthRepo
	issuer *AuthService
	svc    *GoogleOAuthService
	jwtSvc *auth.JWTService
	key    *rsa.PrivateKey
	cfg    config.Config

	// Token endpoint behavior knobs (set before runFlow).
	httpStatus  int // token endpoint HTTP status (default 200)
	omitIDToken bool
	sub         string
	email       string
	name        string
	nonce       string

	// Captured at the mock token endpoint (for assertions/leak checks).
	lastRawIDToken string
	lastCode       string
	lastVerifier   string
	lastSecret     string
}

func newGoogleMockEnv(t *testing.T) *googleMockEnv {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	env := &googleMockEnv{
		repo:       newMockOAuthRepo(),
		key:        key,
		httpStatus: http.StatusOK,
		cfg: config.Config{
			JWTSecret:              "test-secret-key-0123456789abcdef",
			JWTAccessTTL:           15 * time.Minute,
			JWTRefreshTTL:          720 * time.Hour,
			GoogleRedirectURI:      "http://localhost:8080/api/v1/auth/google/callback",
			GoogleLinkRedirectURI:  "http://localhost:8080/api/v1/auth/google/link/callback",
			GoogleOAuthFrontendURL: "http://localhost:3000",
		},
	}
	env.jwtSvc = auth.NewJWTService(env.cfg)
	env.issuer = &AuthService{repo: env.repo, jwt: env.jwtSvc, cfg: env.cfg}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("mock token endpoint: parse form: %v", err)
		}
		env.lastCode = r.Form.Get("code")
		env.lastVerifier = r.Form.Get("code_verifier")
		env.lastSecret = r.Form.Get("client_secret")
		if _, sec, ok := r.BasicAuth(); ok && sec != "" {
			env.lastSecret = sec
		}
		w.Header().Set("Content-Type", "application/json")
		if env.httpStatus != http.StatusOK {
			w.WriteHeader(env.httpStatus)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"mock provider rejection"}`)
			return
		}
		resp := map[string]any{
			"access_token": mockProviderAccessTok,
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if !env.omitIDToken {
			resp["id_token"] = env.signIDToken(t)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	client := auth.NewGoogleClientMockServerForTest(
		mockGoogleClientID, mockGoogleClientSecret, env.cfg.GoogleRedirectURI, srv.URL,
		&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}},
	)
	env.svc = NewGoogleOAuthServiceForTest(env.cfg, env.repo, env.issuer, client)
	return env
}

// signIDToken mints the mocked Google id_token for the current env identity.
func (env *googleMockEnv) signIDToken(t *testing.T) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":            "https://accounts.google.com",
		"aud":            mockGoogleClientID,
		"sub":            env.sub,
		"exp":            time.Now().Add(10 * time.Minute).Unix(),
		"iat":            time.Now().Unix(),
		"email":          env.email,
		"email_verified": true,
		"name":           env.name,
		"nonce":          env.nonce,
	})
	token.Header["kid"] = "mock-kid"
	raw, err := token.SignedString(env.key)
	if err != nil {
		t.Fatalf("sign id_token: %v", err)
	}
	env.lastRawIDToken = raw
	return raw
}

// runFlow performs StartLogin (parsing the state out of the consent URL, as
// the browser round-trip would) and then Callback. It asserts the CSRF state
// binding: the state in the redirect URL must be high-entropy and match a
// persisted state HASH server-side.
func (env *googleMockEnv) runFlow(t *testing.T, linkUserID *uuid.UUID) (GoogleCallbackResult, string, error) {
	t.Helper()
	meta := AuthRequestMeta{IP: "127.0.0.1", UserAgent: "test-agent", RequestID: "req-flow"}
	start, err := env.svc.StartLogin(context.Background(), "/trip/abc", linkUserID, meta)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, err := url.Parse(start.RedirectURL)
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != mockGoogleClientID {
		t.Errorf("consent URL missing/wrong client_id: %q", q.Get("client_id"))
	}
	if q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Error("consent URL missing nonce/code_challenge")
	}
	state := q.Get("state")
	if len(state) < 43 { // 32 random bytes base64url
		t.Fatalf("state too short (%d chars) — weak entropy", len(state))
	}
	row, ok := env.repo.states[hashOAuthState(state)]
	if !ok {
		t.Fatal("redirect state matches no persisted state hash — CSRF binding broken")
	}
	env.nonce = row.Nonce // the mock provider echoes this nonce in the id_token
	res, err := env.svc.Callback(context.Background(), mockAuthCode, state, meta)
	return res, state, err
}

// assertValidSession proves a Callback result carries a NORMAL Vero session:
// access JWT (aud=access, correct user, RoleUser), refresh JWT (aud=refresh,
// JTI), and a persisted revocable AuthSession row.
func (env *googleMockEnv) assertValidSession(t *testing.T, res GoogleCallbackResult, userID uuid.UUID) {
	t.Helper()
	access, err := env.jwtSvc.ParseWithAudience(res.Issue.Response.AccessToken, auth.AudienceAccess)
	if err != nil {
		t.Fatalf("access token not a valid access JWT: %v", err)
	}
	if access.UserID != userID || access.Role != models.RoleUser {
		t.Errorf("access claims wrong: user=%v role=%q", access.UserID, access.Role)
	}
	refresh, err := env.jwtSvc.ParseWithAudience(res.Issue.RefreshToken, auth.AudienceRefresh)
	if err != nil {
		t.Fatalf("refresh token not a valid refresh JWT: %v", err)
	}
	if refresh.ID == "" || refresh.ID != res.Issue.RefreshJTI {
		t.Errorf("refresh JTI missing/mismatch: %q vs %q", refresh.ID, res.Issue.RefreshJTI)
	}
	if _, err := env.repo.FindActiveSessionByJTI(context.Background(), res.Issue.RefreshJTI); err != nil {
		t.Fatalf("AuthSession row not persisted: %v", err)
	}
}

// TestCallbackFullFlow_NewGoogleUser: a first-time Google identity creates a
// RoleUser account keyed by sub AND a normal Vero session end-to-end.
func TestCallbackFullFlow_NewGoogleUser(t *testing.T) {
	env := newGoogleMockEnv(t)
	env.sub, env.email, env.name = "sub-new-user", "new@gmail.com", "New User"

	res, _, err := env.runFlow(t, nil)
	if err != nil {
		t.Fatalf("Callback err: %v", err)
	}
	if res.ReturnTo != "/trip/abc" {
		t.Errorf("ReturnTo = %q, want /trip/abc", res.ReturnTo)
	}
	if env.repo.createdUser == nil {
		t.Fatal("new Google user not created")
	}
	u := env.repo.createdUser
	if u.Role != models.RoleUser {
		t.Errorf("new Google user role = %q, want RoleUser (SEC-1)", u.Role)
	}
	if u.GoogleSub == nil || *u.GoogleSub != env.sub {
		t.Error("new user missing google_sub link")
	}
	// The canonical sub mapping must resolve future logins.
	if got := env.repo.usersBySub[env.sub]; got == nil || got.ID != u.ID {
		t.Error("usersBySub mapping missing — identity not keyed by sub")
	}
	env.assertValidSession(t, res, u.ID)
}

// TestCallbackFullFlow_ExistingGoogleUser: a second login with the same Google
// sub resolves the SAME account (no duplicate user) and issues a fresh session.
func TestCallbackFullFlow_ExistingGoogleUser(t *testing.T) {
	env := newGoogleMockEnv(t)
	env.sub, env.email, env.name = "sub-repeat", "repeat@gmail.com", "Repeat"

	first, _, err := env.runFlow(t, nil)
	if err != nil {
		t.Fatalf("first Callback err: %v", err)
	}
	firstUser := env.repo.createdUser
	if firstUser == nil {
		t.Fatal("first login should create the user")
	}

	// Simulate the user renaming/changing email on the Google side: identity
	// resolution is keyed by sub, so this must still hit the same account.
	env.repo.createdUser = nil
	env.email, env.name = "renamed@gmail.com", "Renamed"
	second, _, err := env.runFlow(t, nil)
	if err != nil {
		t.Fatalf("second Callback err: %v", err)
	}
	if env.repo.createdUser != nil {
		t.Error("duplicate user created on re-login by sub")
	}
	claims, err := env.jwtSvc.ParseWithAudience(second.Issue.Response.AccessToken, auth.AudienceAccess)
	if err != nil {
		t.Fatalf("second access token invalid: %v", err)
	}
	if claims.UserID != firstUser.ID {
		t.Errorf("re-login resolved different user: %v vs %v", claims.UserID, firstUser.ID)
	}
	if second.Issue.RefreshJTI == first.Issue.RefreshJTI {
		t.Error("second login must issue a NEW session (distinct JTI)")
	}
}

// TestCallbackFullFlow_ExistingPasswordUserRefused: a Google login whose email
// matches a pre-existing password account (sub never linked) is REFUSED — no
// auto-merge, no session (account-takeover guard).
func TestCallbackFullFlow_ExistingPasswordUserRefused(t *testing.T) {
	env := newGoogleMockEnv(t)
	pwUser := &models.User{Name: "PW", Email: "pw@x.com", Password: "bcrypt-hash", Role: models.RoleUser}
	pwUser.ID = uuid.New()
	env.repo.usersByEmail[pwUser.Email] = pwUser
	env.repo.usersByID[pwUser.ID] = pwUser

	env.sub, env.email = "sub-never-linked", "pw@x.com"
	_, _, err := env.runFlow(t, nil)
	if !errors.Is(err, ErrGoogleAccountExists) {
		t.Fatalf("expected ErrGoogleAccountExists (no auto-merge), got %v", err)
	}
	if len(env.repo.sessions) != 0 {
		t.Error("session issued for refused login — takeover guard broken")
	}
	if env.repo.createdUser != nil {
		t.Error("user created for refused login")
	}
	if pwUser.GoogleSub != nil {
		t.Error("password account gained a Google sub via login flow")
	}
}

// TestCallbackFullFlow_InvalidProviderResponse: the provider rejecting the
// code exchange must fail the callback WITHOUT creating a user or session.
func TestCallbackFullFlow_InvalidProviderResponse(t *testing.T) {
	env := newGoogleMockEnv(t)
	env.sub, env.email = "sub-x", "x@gmail.com"
	env.httpStatus = http.StatusBadRequest

	_, _, err := env.runFlow(t, nil)
	if !errors.Is(err, auth.ErrGoogleExchangeFailed) {
		t.Fatalf("expected ErrGoogleExchangeFailed, got %v", err)
	}
	if env.repo.createdUser != nil || len(env.repo.sessions) != 0 {
		t.Error("user/session created despite failed exchange")
	}
}

// TestCallbackFullFlow_MissingIDToken: a 200 token response without id_token
// is an invalid provider response — callback fails, nothing persists.
func TestCallbackFullFlow_MissingIDToken(t *testing.T) {
	env := newGoogleMockEnv(t)
	env.sub, env.email = "sub-x", "x@gmail.com"
	env.omitIDToken = true

	_, _, err := env.runFlow(t, nil)
	if !errors.Is(err, auth.ErrGoogleMissingIDToken) {
		t.Fatalf("expected ErrGoogleMissingIDToken, got %v", err)
	}
	if env.repo.createdUser != nil || len(env.repo.sessions) != 0 {
		t.Error("user/session created despite missing id_token")
	}
}

// TestCallbackFullFlow_LinkAuthenticatedUser: the explicit link flow attaches
// a verified Google identity to the ALREADY AUTHENTICATED account (stamped on
// the state) and issues NO new session. A second account attempting to link
// the SAME Google sub is rejected (one Google account → one Vero account;
// ownership is never transferred).
func TestCallbackFullFlow_LinkAuthenticatedUser(t *testing.T) {
	env := newGoogleMockEnv(t)
	owner := &models.User{Name: "Owner", Email: "owner@x.com", Password: "bcrypt-hash", Role: models.RoleUser}
	owner.ID = uuid.New()
	env.repo.usersByEmail[owner.Email] = owner
	env.repo.usersByID[owner.ID] = owner

	env.sub, env.email = "sub-link", "owner@gmail.com"
	res, _, err := env.runFlow(t, &owner.ID)
	if err != nil {
		t.Fatalf("link Callback err: %v", err)
	}
	if res.LinkedUserID() != owner.ID.String() {
		t.Errorf("LinkedUserID = %q, want %q", res.LinkedUserID(), owner.ID)
	}
	if res.Issue.Response.AccessToken != "" || res.Issue.RefreshToken != "" {
		t.Error("link flow must NOT issue a new session")
	}
	if len(env.repo.sessions) != 0 {
		t.Error("link flow created an AuthSession")
	}
	if got := env.repo.usersBySub[env.sub]; got == nil || got.ID != owner.ID {
		t.Error("Google sub not linked to the authenticated account")
	}

	// Conflict: a DIFFERENT authenticated account tries to link the same sub.
	attacker := &models.User{Name: "Atk", Email: "atk@x.com", Password: "bcrypt-hash", Role: models.RoleUser}
	attacker.ID = uuid.New()
	env.repo.usersByEmail[attacker.Email] = attacker
	env.repo.usersByID[attacker.ID] = attacker

	_, _, err = env.runFlow(t, &attacker.ID)
	if !errors.Is(err, ErrGoogleIdentityTaken) {
		t.Fatalf("expected ErrGoogleIdentityTaken, got %v", err)
	}
	if got := env.repo.usersBySub[env.sub]; got.ID != owner.ID {
		t.Error("ownership transferred to attacker — must never happen")
	}
	if attacker.GoogleSub != nil {
		t.Error("attacker account gained a link to another user's Google sub")
	}
}

// TestCallbackFullFlow_RefreshLogoutRevoke: a session created via Google login
// works with the NORMAL session machinery — refresh rotates atomically, the
// rotated-out refresh token is rejected, logout revokes, and refresh after
// logout fails.
func TestCallbackFullFlow_RefreshLogoutRevoke(t *testing.T) {
	env := newGoogleMockEnv(t)
	env.sub, env.email = "sub-sess", "sess@gmail.com"
	res, _, err := env.runFlow(t, nil)
	if err != nil {
		t.Fatalf("Callback err: %v", err)
	}
	meta := AuthRequestMeta{IP: "127.0.0.1", RequestID: "req-refresh"}
	oldJTI := res.Issue.RefreshJTI

	// Refresh works: new token pair, old JTI rotated out.
	refreshed, err := env.issuer.Refresh(context.Background(), res.Issue.RefreshToken, meta)
	if err != nil {
		t.Fatalf("Refresh err: %v", err)
	}
	if refreshed.RefreshJTI == oldJTI {
		t.Error("refresh did not rotate the session JTI")
	}
	if _, err := env.repo.FindActiveSessionByJTI(context.Background(), oldJTI); err == nil {
		t.Error("old session still active after rotation")
	}
	env.assertValidSession(t, GoogleCallbackResult{Issue: refreshed}, env.repo.createdUser.ID)

	// Replaying the rotated-out refresh token is rejected.
	if _, err := env.issuer.Refresh(context.Background(), res.Issue.RefreshToken, meta); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Fatalf("expected ErrRefreshTokenRevoked for rotated token replay, got %v", err)
	}

	// Logout works: revokes the current session.
	if err := env.issuer.Logout(context.Background(), refreshed.RefreshToken, meta); err != nil {
		t.Fatalf("Logout err: %v", err)
	}
	if _, err := env.repo.FindActiveSessionByJTI(context.Background(), refreshed.RefreshJTI); err == nil {
		t.Error("session still active after logout")
	}

	// Refresh after logout is rejected.
	if _, err := env.issuer.Refresh(context.Background(), refreshed.RefreshToken, meta); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Fatalf("expected ErrRefreshTokenRevoked after logout, got %v", err)
	}
}

// TestGoogleAuditEvents_NeverLeakSecrets locks the secret-hygiene contract
// across a FULL successful login AND a failed exchange: neither the client
// secret, provider access token, raw id_token, authorization code, raw state,
// nonce, PKCE verifier, nor the issued Vero tokens may appear in the audit
// log. Only safe identifiers (user_id, email, jti, reason categories) do.
func TestGoogleAuditEvents_NeverLeakSecrets(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	env := newGoogleMockEnv(t)
	env.sub, env.email = "sub-leak", "leak@gmail.com"

	// 1. Successful login (exercises exchange + verify + session issue).
	res, state, err := env.runFlow(t, nil)
	if err != nil {
		t.Fatalf("Callback err: %v", err)
	}
	successIDToken := env.lastRawIDToken

	// 2. Failing exchange (provider rejects the code).
	env.sub, env.email = "sub-leak2", "leak2@gmail.com"
	env.httpStatus = http.StatusBadRequest
	if _, _, err := env.runFlow(t, nil); err == nil {
		t.Fatal("expected failing exchange")
	}

	out := buf.String()
	for _, want := range []string{
		auth.EventGoogleLoginSuccess,
		auth.EventGoogleLoginFailed,
		`"reason":"exchange_or_verify_failed"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("audit log missing %q", want)
		}
	}
	forbidden := []string{
		mockGoogleClientSecret,
		mockProviderAccessTok,
		mockAuthCode,
		state,
		successIDToken,
		res.Issue.Response.AccessToken,
		res.Issue.RefreshToken,
	}
	for _, row := range env.repo.states {
		forbidden = append(forbidden, row.Nonce, row.CodeVerifier)
	}
	for _, secret := range forbidden {
		if secret != "" && strings.Contains(out, secret) {
			t.Errorf("audit log leaks forbidden value %.24q...", secret)
		}
	}
}

// mockGuestRepo is an in-memory repositories.GuestRepository for the
// guest-order-claim test.
type mockGuestRepo struct {
	session      models.GuestSession
	hasSession   bool
	claimed      bool
	claimGuestID uuid.UUID
	claimUserID  uuid.UUID
	claimBooking uuid.UUID
	// Chat→guest binding (GO-P2-7): recorded pair + a switch to simulate a
	// chat session already owned by another live guest identity.
	boundChatID  uuid.UUID
	boundGuestID uuid.UUID
	bindRefused  bool
}

func (m *mockGuestRepo) CreateGuestSession(_ context.Context, s *models.GuestSession) error {
	m.session, m.hasSession = *s, true
	return nil
}
func (m *mockGuestRepo) FindGuestSessionByTokenHash(_ context.Context, _ string) (models.GuestSession, error) {
	if !m.hasSession {
		return models.GuestSession{}, gorm.ErrRecordNotFound
	}
	return m.session, nil
}
func (m *mockGuestRepo) FindGuestSession(_ context.Context, id uuid.UUID) (models.GuestSession, error) {
	if !m.hasSession || m.session.ID != id {
		return models.GuestSession{}, gorm.ErrRecordNotFound
	}
	return m.session, nil
}

// BindChatSessionGuest mirrors the conditional bind (GO-P2-7). The mock has no
// chat sessions, so it reports the first bind as won and remembers the pair.
func (m *mockGuestRepo) BindChatSessionGuest(_ context.Context, chatID, guestID uuid.UUID) (bool, error) {
	m.boundChatID, m.boundGuestID = chatID, guestID
	return !m.bindRefused, nil
}

func (m *mockGuestRepo) ClaimGuestOrder(_ context.Context, guestID, userID uuid.UUID) (repositories.GuestOrderClaim, error) {
	m.claimed, m.claimGuestID, m.claimUserID = true, guestID, userID
	return repositories.GuestOrderClaim{BookingID: m.claimBooking, OwnerID: userID, Transferred: true}, nil
}

// TestGoogleLogin_ClaimsGuestOrder: after a Google login the pre-existing
// guest order (cookie-identified, single-use) is claimed to the now-
// authenticated account — mirroring the password login/register path. A user
// WITHOUT a guest order (or without a guest cookie) is a no-op.
func TestGoogleLogin_ClaimsGuestOrder(t *testing.T) {
	env := newGoogleMockEnv(t)
	env.sub, env.email = "sub-guest", "guest@gmail.com"
	res, _, err := env.runFlow(t, nil)
	if err != nil {
		t.Fatalf("Callback err: %v", err)
	}
	user, ok := res.Issue.Response.User.(models.User)
	if !ok {
		t.Fatal("response does not carry the logged-in user")
	}

	grepo := &mockGuestRepo{}
	guests := &GuestService{repo: grepo, cfg: env.cfg, users: env.issuer}

	// Guest session with a pending first order (the handler passes the raw
	// cookie token; Authenticate resolves it by hash).
	orderID := uuid.New()
	grepo.claimBooking = orderID
	grepo.session = models.GuestSession{
		TokenHash:    "hash-of-cookie-token",
		UserID:       uuid.New(),
		FirstOrderID: &orderID,
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	grepo.session.ID = uuid.New()
	grepo.hasSession = true

	claim, err := guests.ClaimOrder(context.Background(), "cookie-token", user.ID)
	if err != nil {
		t.Fatalf("ClaimOrder err: %v", err)
	}
	if !claim.Transferred || claim.BookingID != grepo.claimBooking {
		t.Errorf("claim result not reported: %+v", claim)
	}
	if !grepo.claimed {
		t.Fatal("guest order not claimed after Google login")
	}
	if grepo.claimUserID != user.ID || grepo.claimGuestID != grepo.session.ID {
		t.Errorf("claimed to wrong target: user=%v guest=%v", grepo.claimUserID, grepo.claimGuestID)
	}

	// No guest cookie → no-op: reported as ErrGuestOrderNothingToClaim (not a
	// failure) and no claim attempt reaches the repository.
	grepo2 := &mockGuestRepo{}
	guests2 := &GuestService{repo: grepo2, cfg: env.cfg, users: env.issuer}
	if _, err := guests2.ClaimOrder(context.Background(), "", user.ID); !errors.Is(err, ErrGuestOrderNothingToClaim) {
		t.Fatalf("ClaimOrder without cookie must report nothing-to-claim, got %v", err)
	}
	if grepo2.claimed {
		t.Error("claim happened without a guest session")
	}
}
