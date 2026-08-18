package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

var ErrGuestSessionInvalid = errors.New("guest session invalid or expired")

type GuestService struct {
	repo  repositories.GuestRepository
	cfg   config.Config
	users GuestUserProvider
}

type GuestIdentity struct {
	Session models.GuestSession
	Token   string
	IsNew   bool
}

func HashGuestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *GuestService) Resolve(ctx context.Context, token string) (GuestIdentity, error) {
	if token != "" {
		session, err := s.repo.FindGuestSessionByTokenHash(ctx, HashGuestToken(token))
		if err == nil {
			return GuestIdentity{Session: session, Token: token}, nil
		}
	}

	ttl := s.cfg.GuestIdentityTTL
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	// Retry on the (astronomically unlikely) token-hash collision or a parallel
	// request that just created the same hash: the second Create hits the
	// unique constraint, then the read-after-collision resolves the winner's row.
	for attempt := 0; attempt < 3; attempt++ {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return GuestIdentity{}, err
		}
		token = hex.EncodeToString(raw)
		user, err := s.users.GuestUser(ctx)
		if err != nil {
			return GuestIdentity{}, err
		}
		session := models.GuestSession{TokenHash: HashGuestToken(token), UserID: user.ID, ExpiresAt: time.Now().Add(ttl)}
		if err := s.repo.CreateGuestSession(ctx, &session); err != nil {
			// Unique-constraint collision (parallel create won the race): resolve
			// the existing session for the same token instead of failing. This
			// also covers a cookie that was cleared between Resolve calls but a
			// session was created concurrently.
			if existing, ferr := s.repo.FindGuestSessionByTokenHash(ctx, HashGuestToken(token)); ferr == nil {
				return GuestIdentity{Session: existing, Token: token}, nil
			}
			continue
		}
		return GuestIdentity{Session: session, Token: token, IsNew: true}, nil
	}
	return GuestIdentity{}, errors.New("failed to create guest session")
}

func (s *GuestService) Authenticate(ctx context.Context, token string) (models.GuestSession, error) {
	if token == "" {
		return models.GuestSession{}, ErrGuestSessionInvalid
	}
	session, err := s.repo.FindGuestSessionByTokenHash(ctx, HashGuestToken(token))
	if err != nil {
		return models.GuestSession{}, ErrGuestSessionInvalid
	}
	return session, nil
}

func (s *GuestService) AttachChat(ctx context.Context, chatID, guestID uuid.UUID) error {
	return s.repo.UpdateChatSessionGuest(ctx, chatID, guestID)
}

func (s *GuestService) ClaimOrder(ctx context.Context, token string, userID uuid.UUID) error {
	guest, err := s.Authenticate(ctx, token)
	if err != nil || guest.FirstOrderID == nil {
		return nil
	}
	bookingID, err := s.repo.ClaimGuestOrder(ctx, guest.ID, userID)
	if err != nil {
		return err
	}
	auth.LogSecurity("guest_order_linked", map[string]any{"guest_session_id": guest.ID.String(), "booking_id": bookingID.String(), "user_id": userID.String()})
	return nil
}
