package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// GoogleOAuthService implements "Continue with Google" (18 Agu 2026) as an
// ADDITIONAL authentication provider. It never bypasses the existing session
// machinery: on a verified Google identity it resolves/creates the Vero user,
// then delegates to AuthService.issueSession so the result is a NORMAL Vero
// session (JWT access+refresh, AuthSession row, rotation/reuse detection,
// logout, revocation, roles — all unchanged).
type GoogleOAuthService struct {
	repo   GoogleOAuthRepository
	issuer *AuthService
	google *auth.GoogleClient
	cfg    config.Config
	// enabled reports whether Google OAuth is configured (endpoint returns 404
	// when false). Mirrored from cfg.GoogleOAuthEnabled for readability.
	enabled bool
}

// GoogleOAuthRepository is the narrow persistence contract (SEC-27): OAuth
// state lifecycle + user lookup/link by Google sub.
type GoogleOAuthRepository interface {
	repositories.OAuthRepository
	repositories.UserRepository
}

// GoogleStartResult carries the consent redirect target.
type GoogleStartResult struct {
	RedirectURL string
}

// GoogleCallbackResult is the outcome of a verified callback. For the LOGIN
// flow it carries a normal Vero session (Issue) plus the validated post-login
// path. For the LINK flow it carries the freshly-linked user (LinkedUserID)
// and no new session (the user already has one).
type GoogleCallbackResult struct {
	Issue    AuthIssueResult
	ReturnTo string
	// linkedUser is non-nil ONLY for the link flow (LinkUserID set on state).
	linkedUser *models.User
}

// LinkedUserID returns the linked account's ID for the link flow, or "" for
// the login flow. Handler uses it to confirm the link without a new session.
func (r GoogleCallbackResult) LinkedUserID() string {
	if r.linkedUser == nil {
		return ""
	}
	return r.linkedUser.ID.String()
}

// oauthStateTTL bounds the login flow. States also expire at consume time, so
// this only needs to outlast a realistic consent-screen visit.
const oauthStateTTL = 10 * time.Minute

// Sentinel errors for the Google flow (SEC-28 — match with errors.Is).
var (
	ErrGoogleOAuthStateInvalid = errors.New("google oauth state invalid")
	ErrGoogleOAuthStateExpired = errors.New("google oauth state expired")
	// ErrGoogleAccountExists: a Vero account with this email exists but the
	// Google sub is not linked. We refuse to auto-merge (takeover guard); the
	// user must link explicitly via LinkAccount after authenticating.
	ErrGoogleAccountExists = errors.New("vero account with this email already exists")
	// ErrGoogleIdentityTaken: this Google sub is already linked to a DIFFERENT
	// Vero account. A Google account can never map to two Vero accounts.
	ErrGoogleIdentityTaken = errors.New("google account already linked to another user")
)

// NewGoogleOAuthService builds the service. The Google OIDC provider is only
// resolved (network discovery) when the feature is enabled; when disabled the
// service stays nil-safe and the handlers answer 404 without any network call.
func NewGoogleOAuthService(cfg config.Config, repo GoogleOAuthRepository, issuer *AuthService) *GoogleOAuthService {
	s := &GoogleOAuthService{repo: repo, issuer: issuer, cfg: cfg, enabled: cfg.GoogleOAuthEnabled}
	if !cfg.GoogleOAuthEnabled {
		return s
	}
	// Discovery hits Google's OIDC document once; use a bounded detached ctx
	// (startup wiring, not a request path — SEC-26).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := auth.NewGoogleClient(ctx, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	if err != nil {
		// Fail closed: log and keep the service disabled rather than panic at
		// startup or run with a half-initialised verifier.
		log.Printf("[google-oauth] provider init failed, Google login disabled: %v", err)
		s.enabled = false
		return s
	}
	s.google = client
	return s
}

// NewGoogleOAuthServiceForTest builds an ENABLED service around an injected
// (mocked) Google client — no OIDC discovery, no network, no real Google
// credentials. Mirrors auth.NewGoogleClientOfflineForTest /
// auth.NewGoogleClientMockServerForTest: lets tests drive StartLogin/Callback
// end-to-end against a fake Google token endpoint. Production wiring must use
// NewGoogleOAuthService.
func NewGoogleOAuthServiceForTest(cfg config.Config, repo GoogleOAuthRepository, issuer *AuthService, client *auth.GoogleClient) *GoogleOAuthService {
	return &GoogleOAuthService{repo: repo, issuer: issuer, google: client, cfg: cfg, enabled: true}
}

// Enabled reports whether Google OAuth is active (drives the 404 guard).
func (s *GoogleOAuthService) Enabled() bool { return s.enabled && s.google != nil }

// StartLogin generates a single-use state + nonce, persists only the state
// HASH, and returns the Google consent URL. returnTo is validated against the
// allowlist here so an attacker-controlled path never reaches the callback.
// linkUserID is nil for the normal login flow; when set (the "Link Google
// Account" flow, called by an authenticated handler), the callback links the
// verified Google sub to that user instead of resolving/creating an account.
//
// Emits the google_login_started audit event. Payload is deliberately limited
// to safe identifiers (ip/user_agent/request_id/flow/link_user_id) — the raw
// state, nonce, and PKCE verifier are secrets of the flow and never logged.
func (s *GoogleOAuthService) StartLogin(ctx context.Context, returnTo string, linkUserID *uuid.UUID, meta AuthRequestMeta) (GoogleStartResult, error) {
	state, err := randomURLToken(32)
	if err != nil {
		return GoogleStartResult{}, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return GoogleStartResult{}, err
	}
	// PKCE: a high-entropy code_verifier is generated server-side and stored in
	// the state row; only its S256 challenge is sent to Google. At exchange the
	// verifier is presented to prove this callback is the same party that
	// started the flow (mitigates authorization-code interception).
	codeVerifier, err := randomURLToken(64)
	if err != nil {
		return GoogleStartResult{}, err
	}
	row := models.OAuthState{
		StateHash:    hashOAuthState(state),
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		ReturnTo:     sanitizeReturnTo(returnTo),
		ExpiresAt:    time.Now().Add(oauthStateTTL),
		LinkUserID:   linkUserID,
	}
	if err := s.repo.CreateOAuthState(ctx, &row); err != nil {
		auth.LogSecurity(auth.EventGoogleLoginStarted, map[string]any{
			"ip":         meta.IP,
			"user_agent": meta.UserAgent,
			"request_id": meta.RequestID,
			"provider":   "google",
			"flow":       googleFlowName(linkUserID),
			"success":    false,
			"reason":     "state_persist_failed",
		})
		return GoogleStartResult{}, err
	}
	startFields := map[string]any{
		"ip":         meta.IP,
		"user_agent": meta.UserAgent,
		"request_id": meta.RequestID,
		"provider":   "google",
		"flow":       googleFlowName(linkUserID),
		"success":    true,
	}
	if linkUserID != nil {
		startFields["link_user_id"] = linkUserID.String()
	}
	auth.LogSecurity(auth.EventGoogleLoginStarted, startFields)
	return GoogleStartResult{RedirectURL: s.google.AuthCodeURLForRedirect(s.callbackRedirectURI(linkUserID != nil), state, nonce, pkceS256Challenge(codeVerifier))}, nil
}

// googleFlowName labels the audit trail: "login" for the normal flow, "link"
// for the authenticated account-linking flow.
func googleFlowName(linkUserID *uuid.UUID) string {
	if linkUserID != nil {
		return "link"
	}
	return "login"
}

// callbackRedirectURI returns the redirect URI the given flow must use. The
// link flow gets its own /google/link/callback endpoint; the login flow uses
// the configured callback. The token endpoint rejects exchanges whose
// redirect_uri differs from the authorization request, so Callback must ask
// for the same value again at exchange time.
func (s *GoogleOAuthService) callbackRedirectURI(linkFlow bool) string {
	if linkFlow && s.cfg.GoogleLinkRedirectURI != "" {
		return s.cfg.GoogleLinkRedirectURI
	}
	return s.cfg.GoogleRedirectURI
}

// pkceS256Challenge derives the RFC 7636 S256 code_challenge from a verifier:
// BASE64URL(SHA256(verifier)), no padding.
func pkceS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Callback validates the state (single-use, anti-CSRF), exchanges the code,
// verifies the Google identity, resolves/links/creates the Vero user, and
// issues a normal Vero session via AuthService.
func (s *GoogleOAuthService) Callback(ctx context.Context, code, state string, meta AuthRequestMeta) (GoogleCallbackResult, error) {
	stateHash := hashOAuthState(state)
	row, ok, err := s.repo.ConsumeOAuthState(ctx, stateHash)
	if err != nil {
		return GoogleCallbackResult{}, err
	}
	if !ok {
		// Dedicated event kept for CSRF-attempt alerting; also counted as a
		// login failure. No raw state is logged — only its fate.
		auth.LogSecurity(auth.EventGoogleOAuthStateInvalid, map[string]any{
			"ip":         meta.IP,
			"user_agent": meta.UserAgent,
			"request_id": meta.RequestID,
			"provider":   "google",
			"success":    false,
		})
		s.logGoogleLoginFailed(meta, "state_invalid")
		return GoogleCallbackResult{}, ErrGoogleOAuthStateInvalid
	}

	identity, err := s.google.ExchangeForRedirect(ctx, s.callbackRedirectURI(row.LinkUserID != nil), code, row.Nonce, row.CodeVerifier)
	if err != nil {
		// Safe reason only: exchange/verify errors may embed provider response
		// detail — never log the raw error (or the code/tokens) here.
		s.logGoogleLoginFailed(meta, "exchange_or_verify_failed")
		return GoogleCallbackResult{}, err
	}

	// Link flow: the state was started by an ALREADY authenticated user who
	// wants to attach this Google identity to their account. Link instead of
	// resolving/creating — this is the secure account-linking path.
	if row.LinkUserID != nil {
		user, linkErr := s.LinkAccount(ctx, row.LinkUserID.String(), identity, meta)
		if linkErr != nil {
			return GoogleCallbackResult{}, linkErr
		}
		return GoogleCallbackResult{ReturnTo: row.ReturnTo, linkedUser: &user}, nil
	}

	user, err := s.resolveUser(ctx, identity, meta)
	if err != nil {
		s.logGoogleLoginFailed(meta, googleResolveFailReason(err))
		return GoogleCallbackResult{}, err
	}

	issue, err := s.issuer.issueSession(ctx, user)
	if err != nil {
		s.logGoogleLoginFailed(meta, "session_issue_failed")
		return GoogleCallbackResult{}, err
	}
	auth.LogSecurity(auth.EventGoogleLoginSuccess, map[string]any{
		"ip":         meta.IP,
		"user_agent": meta.UserAgent,
		"request_id": meta.RequestID,
		"user_id":    user.ID.String(),
		"email":      user.Email,
		"jti":        issue.RefreshJTI,
		"provider":   "google",
		"flow":       "login",
		"success":    true,
	})
	return GoogleCallbackResult{Issue: issue, ReturnTo: row.ReturnTo}, nil
}

// logGoogleLoginFailed emits google_login_failed with only safe identifiers
// and a category reason — never the raw provider error, authorization code,
// or any token.
func (s *GoogleOAuthService) logGoogleLoginFailed(meta AuthRequestMeta, reason string) {
	auth.LogSecurity(auth.EventGoogleLoginFailed, map[string]any{
		"ip":         meta.IP,
		"user_agent": meta.UserAgent,
		"request_id": meta.RequestID,
		"provider":   "google",
		"success":    false,
		"reason":     reason,
	})
}

// googleResolveFailReason maps account-resolution sentinel errors to safe,
// stable category strings for the audit trail.
func googleResolveFailReason(err error) string {
	switch {
	case errors.Is(err, ErrGoogleAccountExists):
		return "account_link_required"
	case errors.Is(err, ErrGoogleIdentityTaken):
		return "identity_taken"
	default:
		return "account_resolution_failed"
	}
}

// resolveUser implements the account-resolution policy. Deliberately there is
// NO automatic email-based merge (account-takeover guard, 19 Agu 2026):
//
//  1. google_sub match (canonical ExternalIdentity) → existing linked account.
//  2. email match but sub NOT linked → REFUSE to merge. Returning the existing
//     account here would let anyone who can produce a Google token for an
//     email they don't truly own hijack a pre-existing Vero password account.
//     The user must instead link explicitly (LinkAccount) after proving they
//     own the Vero account (authenticated). We surface ErrGoogleAccountExists.
//  3. no match at all → create a fresh RoleUser (random CSPRNG password).
func (s *GoogleOAuthService) resolveUser(ctx context.Context, identity auth.GoogleIdentity, meta AuthRequestMeta) (models.User, error) {
	// 1. Stable immutable key first — one Google account → one Vero account.
	user, err := s.repo.FindUserByGoogleSub(ctx, identity.Subject)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}

	// 2. Email exists but this Google sub was never linked. Do NOT auto-merge:
	//    require explicit, authenticated linking (see LinkAccount). Fail closed.
	if _, err = s.repo.FindUserByEmail(ctx, identity.Email); err == nil {
		auth.LogSecurity(auth.EventGoogleLinkRequired, map[string]any{
			"ip":    meta.IP,
			"email": identity.Email,
		})
		return models.User{}, ErrGoogleAccountExists
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}

	// 3. Create new. Password is random+unusable for password login.
	passwordBytes := make([]byte, 16)
	if _, err := rand.Read(passwordBytes); err != nil {
		return models.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(passwordBytes)), bcrypt.DefaultCost)
	if err != nil {
		return models.User{}, err
	}
	name := strings.TrimSpace(identity.Name)
	if name == "" {
		name = strings.Split(identity.Email, "@")[0]
	}
	newUser := models.User{
		Name:      name,
		Email:     identity.Email,
		Password:  string(hash),
		Role:      models.RoleUser, // SEC-1: role never comes from outside.
		GoogleSub: &identity.Subject,
	}
	// Create user + canonical ExternalIdentity (sub→user) atomically. The
	// identity mapping — not email — is the source of truth for future logins.
	if err := s.repo.CreateUserWithGoogleIdentity(ctx, &newUser, identity.Subject, identity.Email, identity.Picture); err != nil {
		// TOCTOU window (P1-H1): steps 1-2 above are reads, this is the write,
		// and a parallel Google callback or POST /auth/register for the same
		// email can commit in between. The fallback may therefore only
		// re-resolve through the SAME key the primary lookup used — the Google
		// sub, enforced by UNIQUE(provider, provider_user_id).
		if existing, findErr := s.repo.FindUserByGoogleSub(ctx, identity.Subject); findErr == nil {
			return existing, nil
		}
		// Sub still unlinked ⇒ the create lost on users.email UNIQUE against an
		// account that is NOT this Google identity. Resolving by email here
		// (the old behaviour) handed the caller a session on that account,
		// bypassing the anti-merge guard at step 2 — and the guest-order claim
		// that runs right after the callback then moved the CALLER's guest order
		// into it. Return the identical decision the pre-create guard makes, so
		// the outcome never depends on who won the race.
		if _, findErr := s.repo.FindUserByEmail(ctx, identity.Email); findErr == nil {
			auth.LogSecurity(auth.EventGoogleLinkRequired, map[string]any{
				"ip":         meta.IP,
				"request_id": meta.RequestID,
				"email":      identity.Email,
				"reason":     "create_race_email_taken",
			})
			return models.User{}, ErrGoogleAccountExists
		}
		return models.User{}, err
	}
	auth.LogSecurity(auth.EventGoogleAccountCreated, map[string]any{
		"ip":         meta.IP,
		"request_id": meta.RequestID,
		"user_id":    newUser.ID.String(),
		"email":      newUser.Email,
		"provider":   "google",
		"success":    true,
	})
	return newUser, nil
}

// LinkAccount explicitly attaches a VERIFIED Google identity to an ALREADY
// AUTHENTICATED Vero account. This is the secure alternative to email
// auto-merge: the caller must have proven ownership of the Vero account
// (valid access token) AND of the Google account (verified id_token via the
// link flow's own OAuth state). Guards:
//   - the Google sub must not already belong to a DIFFERENT account
//     (ErrGoogleIdentityTaken) — one Google account → one Vero account;
//   - the Google sub must not already be linked to THIS account (idempotent
//     no-op, returns the user unchanged);
//   - the link only writes the identity mapping; it never touches role or
//     password (SEC-1 — role stays server-side).
func (s *GoogleOAuthService) LinkAccount(ctx context.Context, userID string, identity auth.GoogleIdentity, meta AuthRequestMeta) (models.User, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return models.User{}, err
	}
	user, err := s.repo.FindUserByID(ctx, uid)
	if err != nil {
		return models.User{}, err
	}
	// Already linked to some account?
	existing, err := s.repo.FindUserByGoogleSub(ctx, identity.Subject)
	if err == nil {
		if existing.ID == user.ID {
			return user, nil // idempotent: same account re-linking
		}
		auth.LogSecurity(auth.EventGoogleLoginFailed, map[string]any{
			"ip":         meta.IP,
			"request_id": meta.RequestID,
			"user_id":    user.ID.String(),
			"provider":   "google",
			"flow":       "link",
			"success":    false,
			"reason":     "identity_taken",
		})
		return models.User{}, ErrGoogleIdentityTaken
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}
	if linkErr := s.repo.LinkUserGoogleSub(ctx, user.ID.String(), identity.Subject, identity.Email, identity.Picture); linkErr != nil {
		// Same TOCTOU shape as resolveUser: the "already linked?" check above is
		// a read, this is the write, and a parallel link of the SAME sub can
		// commit in between. UNIQUE(provider, provider_user_id) is what actually
		// decides the winner, so re-resolve through that same key and answer with
		// the decision the pre-check would have produced instead of a generic
		// failure — the outcome must not depend on who won.
		if existing, findErr := s.repo.FindUserByGoogleSub(ctx, identity.Subject); findErr == nil {
			if existing.ID == user.ID {
				// The parallel winner was this same account: idempotent success.
				user.GoogleSub = &identity.Subject
				return user, nil
			}
			auth.LogSecurity(auth.EventGoogleLoginFailed, map[string]any{
				"ip":         meta.IP,
				"request_id": meta.RequestID,
				"user_id":    user.ID.String(),
				"provider":   "google",
				"flow":       "link",
				"success":    false,
				"reason":     "identity_taken",
			})
			return models.User{}, ErrGoogleIdentityTaken
		}
		return models.User{}, linkErr
	}
	auth.LogSecurity(auth.EventGoogleAccountLinked, map[string]any{
		"ip":         meta.IP,
		"request_id": meta.RequestID,
		"user_id":    user.ID.String(),
		"email":      user.Email,
		"provider":   "google",
		"flow":       "link",
		"success":    true,
	})
	user.GoogleSub = &identity.Subject
	return user, nil
}

// hashOAuthState stores only the SHA-256 digest of the raw state so a leaked
// oauth_states table cannot be replayed as a valid state parameter.
func hashOAuthState(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// randomURLToken returns n random bytes base64url-encoded (CSPRNG).
func randomURLToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// sanitizeReturnTo allowlists post-login paths: only absolute site-relative
// paths are accepted ("/trip/x"), never scheme/host-relative URLs ("//evil").
// Anything else falls back to "/". This is the open-redirect guard.
//
// Backslashes are rejected outright: browsers normalize "\" to "/" when
// parsing a Location header, so "/\evil.com" would be navigated as the
// protocol-relative "//evil.com" — an open redirect despite the "//" check.
func sanitizeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	if strings.ContainsAny(value, "\r\n\\") {
		return "/"
	}
	return value
}
