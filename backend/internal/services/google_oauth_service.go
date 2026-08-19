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

// GoogleCallbackResult is the outcome of a verified callback: a normal Vero
// session (same shape as password login) plus the validated post-login path.
type GoogleCallbackResult struct {
	Issue    AuthIssueResult
	ReturnTo string
}

// oauthStateTTL bounds the login flow. States also expire at consume time, so
// this only needs to outlast a realistic consent-screen visit.
const oauthStateTTL = 10 * time.Minute

// Sentinel errors for the Google flow (SEC-28 — match with errors.Is).
var (
	ErrGoogleOAuthStateInvalid = errors.New("google oauth state invalid")
	ErrGoogleOAuthStateExpired = errors.New("google oauth state expired")
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

// Enabled reports whether Google OAuth is active (drives the 404 guard).
func (s *GoogleOAuthService) Enabled() bool { return s.enabled && s.google != nil }

// StartLogin generates a single-use state + nonce, persists only the state
// HASH, and returns the Google consent URL. returnTo is validated against the
// allowlist here so an attacker-controlled path never reaches the callback.
func (s *GoogleOAuthService) StartLogin(ctx context.Context, returnTo string) (GoogleStartResult, error) {
	state, err := randomURLToken(32)
	if err != nil {
		return GoogleStartResult{}, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return GoogleStartResult{}, err
	}
	row := models.OAuthState{
		StateHash: hashOAuthState(state),
		Nonce:     nonce,
		ReturnTo:  sanitizeReturnTo(returnTo),
		ExpiresAt: time.Now().Add(oauthStateTTL),
	}
	if err := s.repo.CreateOAuthState(ctx, &row); err != nil {
		return GoogleStartResult{}, err
	}
	return GoogleStartResult{RedirectURL: s.google.AuthCodeURL(state, nonce)}, nil
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
		auth.LogSecurity(auth.EventGoogleOAuthStateInvalid, map[string]any{
			"ip":         meta.IP,
			"user_agent": meta.UserAgent,
			"request_id": meta.RequestID,
		})
		return GoogleCallbackResult{}, ErrGoogleOAuthStateInvalid
	}

	identity, err := s.google.Exchange(ctx, code, row.Nonce)
	if err != nil {
		auth.LogSecurity(auth.EventGoogleLoginFailed, map[string]any{
			"ip":         meta.IP,
			"user_agent": meta.UserAgent,
			"request_id": meta.RequestID,
			"error":      err.Error(),
		})
		return GoogleCallbackResult{}, err
	}

	user, err := s.resolveUser(ctx, identity, meta)
	if err != nil {
		return GoogleCallbackResult{}, err
	}

	issue, err := s.issuer.issueSession(ctx, user)
	if err != nil {
		return GoogleCallbackResult{}, err
	}
	auth.LogSecurity(auth.EventLoginSuccess, map[string]any{
		"ip":         meta.IP,
		"user_agent": meta.UserAgent,
		"request_id": meta.RequestID,
		"user_id":    user.ID.String(),
		"email":      user.Email,
		"jti":        issue.RefreshJTI,
		"provider":   "google",
	})
	return GoogleCallbackResult{Issue: issue, ReturnTo: row.ReturnTo}, nil
}

// resolveUser implements the account-linking policy:
//  1. google_sub match → existing linked account.
//  2. verified-email match → link google_sub to the existing account (the
//     password login keeps working; the account is not locked to Google).
//  3. no match → create a plain RoleUser with a random unguessable password
//     (same CSPRNG+bcrypt pattern as GuestUser, SEC-24) so the not-null
//     password constraint stays satisfied.
func (s *GoogleOAuthService) resolveUser(ctx context.Context, identity auth.GoogleIdentity, meta AuthRequestMeta) (models.User, error) {
	// 1. Stable immutable key first.
	user, err := s.repo.FindUserByGoogleSub(ctx, identity.Subject)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}

	// 2. Link by verified email.
	user, err = s.repo.FindUserByEmail(ctx, identity.Email)
	if err == nil {
		if linkErr := s.repo.LinkUserGoogleSub(ctx, user.ID.String(), identity.Subject); linkErr != nil {
			return models.User{}, linkErr
		}
		auth.LogSecurity(auth.EventGoogleAccountLinked, map[string]any{
			"ip":      meta.IP,
			"user_id": user.ID.String(),
			"email":   user.Email,
		})
		// Reflect the link on the returned struct (jwt.Generate embeds user).
		user.GoogleSub = &identity.Subject
		return user, nil
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
	if err := s.repo.CreateUser(ctx, &newUser); err != nil {
		// Race: a parallel callback created the same email/sub first. Fall back
		// to the now-existing row so the second login still succeeds.
		if existing, findErr := s.repo.FindUserByGoogleSub(ctx, identity.Subject); findErr == nil {
			return existing, nil
		}
		if existing, findErr := s.repo.FindUserByEmail(ctx, identity.Email); findErr == nil {
			return existing, nil
		}
		return models.User{}, err
	}
	auth.LogSecurity(auth.EventGoogleAccountCreated, map[string]any{
		"ip":      meta.IP,
		"user_id": newUser.ID.String(),
		"email":   newUser.Email,
	})
	return newUser, nil
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
func sanitizeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	if strings.ContainsAny(value, "\r\n") {
		return "/"
	}
	return value
}
