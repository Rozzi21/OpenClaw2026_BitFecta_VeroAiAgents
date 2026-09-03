package services_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/gorm"
)

// Guest one-order enforcement — contact-anchored entitlement (GO-P0-1).
//
// The tests in guest_order_limit_test.go cover the cookie-anchored half
// (guest_sessions.order_count). These cover the half that makes the rule
// authoritative when the client throws its identity away: entitlement is keyed
// on the normalized contact of the order, so re-minting a guest session — which
// is exactly what clearing the cookie, opening a private window, using another
// tab, wiping localStorage or calling the API without a cookie jar produces —
// does not hand out a second order.

const (
	anchorEmail = "Guest.Order@Example.com"
	anchorPhone = "0812-3456-789"
)

// newGuestIdentity mints an independent guest identity, i.e. what
// GuestService.Resolve does for a request that arrives without a usable
// vero_guest_session cookie.
func newGuestIdentity(t *testing.T, db *gorm.DB, label string) (models.User, models.GuestSession) {
	t.Helper()
	user := models.User{Name: "Guest " + label, Email: "guest-" + uuid.NewString() + "@vero.local", Password: "x", Role: models.RoleUser}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create guest user %s: %v", label, err)
	}
	guest := models.GuestSession{TokenHash: services.HashGuestToken("token-" + label + "-" + uuid.NewString()), UserID: user.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create guest session %s: %v", label, err)
	}
	return user, guest
}

func contactBookingReq(trip models.Trip, email, phone string) dto.BookingRequest {
	req := validBookingReq(trip)
	req.ContactEmail = email
	req.ContactPhone = phone
	return req
}

func countRows(t *testing.T, db *gorm.DB, model interface{}, query string, args ...interface{}) int64 {
	t.Helper()
	var count int64
	q := db.Model(model)
	if query != "" {
		q = q.Where(query, args...)
	}
	if err := q.Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func TestGuestOrderDeniedForFreshIdentityWithSameContact(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	firstUser, trip, firstGuest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	if _, err := svc.CreateGuest(ctx, firstUser.ID, firstGuest.ID, "idem-anchor-000000001", contactBookingReq(trip, anchorEmail, anchorPhone)); err != nil {
		t.Fatalf("first guest order should succeed: %v", err)
	}
	// One anchor per channel supplied, so neither half of the contact can be
	// reused on its own later.
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 2 {
		t.Fatalf("expected email + phone anchors, got %d", got)
	}

	// Cookie thrown away -> brand new guest session with order_count = 0. Only
	// the contact anchor can stop this request, and it must.
	secondUser, secondGuest := newGuestIdentity(t, repo.DB, "recycled")
	_, err := svc.CreateGuest(ctx, secondUser.ID, secondGuest.ID, "idem-anchor-000000002",
		contactBookingReq(trip, "guest.order+another@example.com", "+62 812 3456 789"))
	if !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("re-minted identity with same contact must be denied, got %v", err)
	}

	// Each channel blocks on its own: email only...
	thirdUser, thirdGuest := newGuestIdentity(t, repo.DB, "email-only")
	if _, err := svc.CreateGuest(ctx, thirdUser.ID, thirdGuest.ID, "idem-anchor-000000003",
		contactBookingReq(trip, "GUEST.ORDER@example.com", "")); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("same email under a new identity must be denied, got %v", err)
	}
	// ...and phone only, written in a different but equivalent format.
	fourthUser, fourthGuest := newGuestIdentity(t, repo.DB, "phone-only")
	if _, err := svc.CreateGuest(ctx, fourthUser.ID, fourthGuest.ID, "idem-anchor-000000004",
		contactBookingReq(trip, "", "0062 812 3456 789")); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("same phone under a new identity must be denied, got %v", err)
	}

	if got := countRows(t, repo.DB, &models.Booking{}, ""); got != 1 {
		t.Fatalf("expected exactly one guest booking, got %d", got)
	}
	// A genuinely different visitor is not blocked.
	fifthUser, fifthGuest := newGuestIdentity(t, repo.DB, "other-person")
	if _, err := svc.CreateGuest(ctx, fifthUser.ID, fifthGuest.ID, "idem-anchor-000000005",
		contactBookingReq(trip, "someone.else@example.com", "081999888777")); err != nil {
		t.Fatalf("unrelated guest must still get their first order: %v", err)
	}
}

// Identity rotation is the silent variant of a cleared cookie: guest_sessions
// rows expire (GUEST_IDENTITY_TTL_HOURS) while the cookie keeps being refreshed,
// so Resolve eventually mints a new row. The entitlement must not come back.
func TestGuestEntitlementSurvivesIdentityRotation(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-rotate-000000001", contactBookingReq(trip, anchorEmail, anchorPhone)); err != nil {
		t.Fatalf("first guest order: %v", err)
	}
	// Age the old identity out completely.
	if err := repo.DB.Model(&models.GuestSession{}).Where("id = ?", guest.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire guest session: %v", err)
	}
	rotatedUser, rotatedGuest := newGuestIdentity(t, repo.DB, "rotated")
	if _, err := svc.CreateGuest(ctx, rotatedUser.ID, rotatedGuest.ID, "idem-rotate-000000002",
		contactBookingReq(trip, anchorEmail, anchorPhone)); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("rotated identity with same contact must be denied, got %v", err)
	}
}

// A new ChatSession — new conversation, new tab, restored history — is only a
// link to the identity. It carries no entitlement state, so it can neither
// restore nor spend one.
func TestNewChatSessionDoesNotResetGuestEntitlement(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	firstChat := models.ChatSession{Title: "first", GuestSessionID: &guest.ID}
	if err := repo.DB.Create(&firstChat).Error; err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-chat-00000000001", contactBookingReq(trip, anchorEmail, anchorPhone)); err != nil {
		t.Fatalf("first guest order: %v", err)
	}

	// Same identity, brand new conversation.
	secondChat := models.ChatSession{Title: "second", GuestSessionID: &guest.ID}
	if err := repo.DB.Create(&secondChat).Error; err != nil {
		t.Fatalf("create second chat session: %v", err)
	}
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-chat-00000000002", contactBookingReq(trip, anchorEmail, anchorPhone)); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("new chat session must not reset entitlement, got %v", err)
	}

	// New conversation AND new identity, which is what a fresh browser profile
	// produces. Still denied, now by the contact anchor.
	freshUser, freshGuest := newGuestIdentity(t, repo.DB, "fresh-chat")
	freshChat := models.ChatSession{Title: "fresh", GuestSessionID: &freshGuest.ID}
	if err := repo.DB.Create(&freshChat).Error; err != nil {
		t.Fatalf("create fresh chat session: %v", err)
	}
	if _, err := svc.CreateGuest(ctx, freshUser.ID, freshGuest.ID, "idem-chat-00000000003", contactBookingReq(trip, anchorEmail, anchorPhone)); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("new chat + new identity must not reset entitlement, got %v", err)
	}
}

func TestFailedGuestOrderDoesNotConsumeContactEntitlement(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	unknownTrip := contactBookingReq(trip, anchorEmail, anchorPhone)
	unknownTrip.TripID = uuid.New()
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-failed-00000001", unknownTrip); err == nil {
		t.Fatal("order for an unknown trip must fail")
	}
	badDate := contactBookingReq(trip, anchorEmail, anchorPhone)
	badDate.TravelDate = "not-a-date"
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-failed-00000002", badDate); !errors.Is(err, services.ErrBookingTravelDateInvalid) {
		t.Fatalf("invalid travel date must fail validation, got %v", err)
	}
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 0 {
		t.Fatalf("failed attempts consumed %d contact anchors", got)
	}

	// The contact is still eligible — even from a different identity.
	otherUser, otherGuest := newGuestIdentity(t, repo.DB, "after-failure")
	if _, err := svc.CreateGuest(ctx, otherUser.ID, otherGuest.ID, "idem-failed-00000003", contactBookingReq(trip, anchorEmail, anchorPhone)); err != nil {
		t.Fatalf("contact must still be entitled after failed attempts: %v", err)
	}
	// ...and is spent exactly once.
	thirdUser, thirdGuest := newGuestIdentity(t, repo.DB, "after-success")
	if _, err := svc.CreateGuest(ctx, thirdUser.ID, thirdGuest.ID, "idem-failed-00000004", contactBookingReq(trip, anchorEmail, anchorPhone)); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("contact must be spent after the successful order, got %v", err)
	}
}

// Without an anchorable contact the rule would silently fall back to
// "one order per cookie", so an unusable contact is rejected as a validation
// failure (which consumes nothing) instead of being let through.
func TestGuestOrderRequiresAnchorableContact(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	junk := contactBookingReq(trip, "not-an-address", "no-digits-here")
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-junk-00000000001", junk); !errors.Is(err, services.ErrBookingContactRequired) {
		t.Fatalf("unusable contact must fail validation, got %v", err)
	}
	if got := countRows(t, repo.DB, &models.GuestSession{}, "id = ? AND order_count = 0", guest.ID); got != 1 {
		t.Fatal("rejected request must not consume the session allowance")
	}
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-junk-00000000002", contactBookingReq(trip, "", anchorPhone)); err != nil {
		t.Fatalf("guest must still get the first order with a usable contact: %v", err)
	}
}

// Concurrent guest requests cannot both create an order, even when each one
// carries its own freshly minted identity (so guest_sessions.order_count cannot
// help). The unique index on contact_key is the arbiter; the loser's booking
// INSERT is rolled back with it.
//
// Caveat, same as TestConcurrentGuestOrdersCreateOnlyOne: the SQLite test DB
// runs with SetMaxOpenConns(1), so these transactions serialize instead of truly
// overlapping (GO-P3-6). What is verified here is the outcome — one winner, one
// booking — and that a losing INSERT leaves nothing behind.
func TestConcurrentGuestIdentitiesSameContactCreateOnlyOne(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	_, trip, _ := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const workers = 8
	users := make([]models.User, 0, workers)
	guests := make([]models.GuestSession, 0, workers)
	for i := 0; i < workers; i++ {
		user, guest := newGuestIdentity(t, repo.DB, fmt.Sprintf("race-%d", i))
		users = append(users, user)
		guests = append(guests, guest)
	}

	var successes, limits atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateGuest(ctx, users[i].ID, guests[i].ID, fmt.Sprintf("idem-anchor-race-%03d", i),
				contactBookingReq(trip, anchorEmail, anchorPhone))
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, services.ErrGuestOrderLimitReached):
				limits.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("expected exactly one success, got %d (limits=%d)", successes.Load(), limits.Load())
	}
	if got := countRows(t, repo.DB, &models.Booking{}, ""); got != 1 {
		t.Fatalf("race created %d bookings", got)
	}
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 2 {
		t.Fatalf("race left %d contact anchors, expected the winner's two", got)
	}
	if got := countRows(t, repo.DB, &models.GuestSession{}, "order_count = 1"); got != 1 {
		t.Fatalf("%d guest sessions consumed their allowance", got)
	}
}

// Authenticated bookings keep the normal booking rules: the guest contact
// anchors neither block them nor grow because of them.
func TestAuthenticatedBookingIgnoresGuestContactAnchors(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-auth-00000000001", contactBookingReq(trip, anchorEmail, anchorPhone)); err != nil {
		t.Fatalf("guest first order: %v", err)
	}
	account := models.User{Name: "Account", Email: "acct-" + uuid.NewString() + "@example.com", Password: "x", Role: models.RoleUser}
	if err := repo.DB.Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	first, err := svc.Create(ctx, account.ID, "idem-auth-00000000002", contactBookingReq(trip, anchorEmail, anchorPhone))
	if err != nil {
		t.Fatalf("authenticated order with an already-spent guest contact must be allowed: %v", err)
	}
	second, err := svc.Create(ctx, account.ID, "idem-auth-00000000003", contactBookingReq(trip, anchorEmail, anchorPhone))
	if err != nil {
		t.Fatalf("authenticated second order must be allowed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("authenticated orders should be distinct")
	}
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 2 {
		t.Fatalf("authenticated bookings must not write contact anchors, found %d", got)
	}
}

// The unique index — not the read that precedes it in BookingService — is what
// keeps two concurrent guests from both persisting an order. This locks in that
// a contact_key which is already taken is REPORTED as a conflict on the driver
// in use (RowsAffected == 0 -> gorm.ErrDuplicatedKey) instead of silently
// succeeding, and that the conflicting INSERT writes nothing.
func TestConsumeGuestOrderEntitlementsRejectsTakenContactKey(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	ctx := context.Background()
	key := strings.Repeat("a", 64)

	if err := repo.ConsumeGuestOrderEntitlements(ctx, []models.GuestOrderEntitlement{
		{ContactKey: key, Channel: models.GuestContactChannelEmail, BookingID: uuid.New()},
	}); err != nil {
		t.Fatalf("first consume must succeed: %v", err)
	}
	err := repo.ConsumeGuestOrderEntitlements(ctx, []models.GuestOrderEntitlement{
		{ContactKey: key, Channel: models.GuestContactChannelEmail, BookingID: uuid.New()},
	})
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("taken contact key must be reported as a conflict, got %v", err)
	}
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 1 {
		t.Fatalf("conflict wrote %d rows, want 1", got)
	}
}
