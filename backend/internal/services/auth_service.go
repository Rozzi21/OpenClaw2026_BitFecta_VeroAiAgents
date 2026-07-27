package services

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	repo *repositories.Repository
	jwt  *auth.JWTService
	cfg  config.Config
}

// refreshRotationConcurrentWindow bounds how recently a session must have been
// revoked to be treated as a benign concurrent-refresh race loser rather than
// token theft/reuse. Two tabs auto-refreshing within milliseconds lose the
// rotation race; a revoked token resurfacing minutes later signals reuse.
const refreshRotationConcurrentWindow = 1 * time.Minute

func (s *AuthService) auditFields(meta AuthRequestMeta, extra map[string]any) map[string]any {
	fields := map[string]any{
		"ip":         meta.IP,
		"user_agent": meta.UserAgent,
		"request_id": meta.RequestID,
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

func (s *AuthService) Register(req dto.RegisterRequest, meta AuthRequestMeta) (AuthIssueResult, error) {
	// SEC-1: public registration must never honor a client-supplied role.
	// Self-service signups are always plain users. Operator/admin accounts are
	// created exclusively via the protected AuthService.CreateStaff path.
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthIssueResult{}, err
	}
	user := models.User{Name: req.Name, Email: strings.ToLower(req.Email), Password: string(hash), Role: models.RoleUser}
	if err := s.repo.CreateUser(&user); err != nil {
		return AuthIssueResult{}, err
	}
	result, err := s.issueSession(user)
	if err != nil {
		return AuthIssueResult{}, err
	}
	auth.LogSecurity(auth.EventLoginSuccess, s.auditFields(meta, map[string]any{
		"user_id": user.ID.String(),
		"email":   user.Email,
		"jti":     result.RefreshJTI,
	}))
	return result, nil
}

// CreateStaff provisions an operator/admin account. It is only reachable through
// an admin-guarded endpoint, so the role here is trusted. Returns the created
// user without issuing a session (the new staff logs in separately).
func (s *AuthService) CreateStaff(req dto.AdminCreateUserRequest, meta AuthRequestMeta) (models.User, error) {
	role := models.Role(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != models.RoleOperator && role != models.RoleAdmin && role != models.RoleUser {
		return models.User{}, errors.New("role must be one of user, operator, admin")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return models.User{}, err
	}
	user := models.User{Name: req.Name, Email: strings.ToLower(req.Email), Password: string(hash), Role: role}
	if err := s.repo.CreateUser(&user); err != nil {
		return models.User{}, err
	}
	auth.LogSecurity("staff_account_created", s.auditFields(meta, map[string]any{
		"user_id": user.ID.String(),
		"email":   user.Email,
		"role":    string(role),
	}))
	return user, nil
}

func (s *AuthService) Login(req dto.LoginRequest, meta AuthRequestMeta) (AuthIssueResult, error) {
	email := req.Email
	if email == "" {
		email = req.Username
	}
	user, err := s.repo.FindUserByEmail(strings.ToLower(email))
	if err != nil {
		auth.LogSecurity(auth.EventLoginFailed, s.auditFields(meta, map[string]any{
			"email": strings.ToLower(email),
			"error": "invalid email or password",
		}))
		return AuthIssueResult{}, errors.New("invalid email or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		auth.LogSecurity(auth.EventLoginFailed, s.auditFields(meta, map[string]any{
			"user_id": user.ID.String(),
			"email":   user.Email,
			"error":   "invalid email or password",
		}))
		return AuthIssueResult{}, errors.New("invalid email or password")
	}
	result, err := s.issueSession(user)
	if err != nil {
		return AuthIssueResult{}, err
	}
	auth.LogSecurity(auth.EventLoginSuccess, s.auditFields(meta, map[string]any{
		"user_id": user.ID.String(),
		"email":   user.Email,
		"jti":     result.RefreshJTI,
	}))
	return result, nil
}

func (s *AuthService) Refresh(refreshToken string, meta AuthRequestMeta) (AuthIssueResult, error) {
	if refreshToken == "" {
		auth.LogSecurity(auth.EventRefreshFailed, s.auditFields(meta, map[string]any{
			"error": "missing refresh token",
		}))
		return AuthIssueResult{}, ErrInvalidRefreshToken
	}

	claims, err := s.jwt.Parse(refreshToken)
	if err == nil && auth.IsAudience(claims, auth.AudienceAccess) {
		auth.LogSecurity(auth.EventAccessTokenUsedOnRefresh, s.auditFields(meta, map[string]any{
			"user_id": claims.UserID.String(),
			"email":   claims.Email,
			"jti":     claims.ID,
		}))
		return AuthIssueResult{}, ErrInvalidRefreshToken
	}

	claims, err = s.jwt.ParseWithAudience(refreshToken, auth.AudienceRefresh)
	if err != nil {
		auth.LogSecurity(auth.EventRefreshFailed, s.auditFields(meta, map[string]any{
			"error": err.Error(),
		}))
		return AuthIssueResult{}, ErrInvalidRefreshToken
	}

	// BUG-1 fix: rotate the session atomically in a single UPDATE. Only the
	// request that wins the race (rowsAffected == 1) proceeds to issue a new
	// token pair; concurrent duplicate refreshes lose the race and are rejected
	// WITHOUT triggering reuse-detection revoke-all (they are not theft).
	rotated, err := s.repo.RotateSession(claims.ID)
	if err != nil {
		return AuthIssueResult{}, err
	}
	if !rotated {
		// Lost the rotation race: the session is already revoked (by a parallel
		// refresh or earlier rotation), expired, or unknown. We must distinguish
		// a benign concurrent refresh (recent rotation) from genuine token reuse
		// (a revoked token surfacing long after rotation = theft signal).
		session, findErr := s.repo.FindSessionByJTI(claims.ID)
		logFields := map[string]any{
			"user_id": claims.UserID.String(),
			"email":   claims.Email,
			"jti":     claims.ID,
		}
		switch {
		case findErr != nil:
			logFields["error"] = "session not found"
			auth.LogSecurity(auth.EventRefreshFailed, s.auditFields(meta, logFields))
		case session.RevokedAt != nil:
			// Rotation happened recently → almost certainly a concurrent refresh
			// race loser (e.g. two tabs auto-refreshing). Reject WITHOUT
			// revoke-all; the rotation winner holds the valid new token.
			// Rotation is stale (longer than the concurrent window) → treat as
			// reuse/theft and defensively revoke all sessions.
			if time.Since(*session.RevokedAt) <= refreshRotationConcurrentWindow {
				logFields["error"] = "concurrent refresh: session already rotated"
				auth.LogSecurity(auth.EventRefreshFailed, s.auditFields(meta, logFields))
			} else {
				_ = s.repo.RevokeAllActiveSessionsByUser(claims.UserID)
				auth.LogSecurity(auth.EventRefreshTokenReuseDetected, s.auditFields(meta, logFields))
			}
		default:
			logFields["error"] = "session expired"
			auth.LogSecurity(auth.EventRefreshTokenRevoked, s.auditFields(meta, logFields))
		}
		return AuthIssueResult{}, ErrRefreshTokenRevoked
	}

	user, err := s.repo.FindUserByID(claims.UserID)
	if err != nil {
		return AuthIssueResult{}, err
	}

	result, err := s.issueSession(user)
	if err != nil {
		return AuthIssueResult{}, err
	}

	auth.LogSecurity(auth.EventRefreshSuccess, s.auditFields(meta, map[string]any{
		"user_id": user.ID.String(),
		"email":   user.Email,
		"jti":     result.RefreshJTI,
	}))
	return result, nil
}

func (s *AuthService) Logout(refreshToken string, meta AuthRequestMeta) error {
	if refreshToken == "" {
		return nil
	}

	claims, err := s.jwt.ParseWithAudience(refreshToken, auth.AudienceRefresh)
	if err != nil {
		auth.LogSecurity(auth.EventLogout, s.auditFields(meta, map[string]any{
			"error": err.Error(),
		}))
		return nil
	}

	_ = s.repo.RevokeSessionByJTIIfExists(claims.ID)
	auth.LogSecurity(auth.EventLogout, s.auditFields(meta, map[string]any{
		"user_id": claims.UserID.String(),
		"email":   claims.Email,
		"jti":     claims.ID,
	}))
	return nil
}

func (s *AuthService) Me(userID uuid.UUID) (models.User, error) {
	return s.repo.FindUserByID(userID)
}

// GuestUser now generates an isolated user per guest booking (Fix for #8).
// We no longer share the `guest@vero.local` dummy user across all guest orders.
func (s *AuthService) GuestUser() (models.User, error) {
	guestID := uuid.NewString()
	hash, _ := bcrypt.GenerateFromPassword([]byte(uuid.NewString()), bcrypt.DefaultCost)
	user := models.User{
		Name:     "Guest Traveler",
		Email:    "guest-" + guestID[:8] + "@vero.local",
		Password: string(hash),
		Role:     models.RoleUser,
	}
	err := s.repo.FirstOrCreateUser(&user)
	return user, err
}

func (s *AuthService) issueSession(user models.User) (AuthIssueResult, error) {
	pair, err := s.jwt.Generate(user)
	if err != nil {
		return AuthIssueResult{}, err
	}
	expiresAt := time.Now().Add(s.jwt.RefreshTTL())
	if err := s.repo.CreateAuthSession(user.ID, pair.RefreshJTI, expiresAt); err != nil {
		return AuthIssueResult{}, err
	}
	return AuthIssueResult{
		Response: dto.AuthResponse{
			AccessToken: pair.AccessToken,
			TokenType:   "Bearer",
			ExpiresIn:   pair.ExpiresIn,
			User:        user,
		},
		RefreshToken: pair.RefreshToken,
		RefreshJTI:   pair.RefreshJTI,
	}, nil
}
