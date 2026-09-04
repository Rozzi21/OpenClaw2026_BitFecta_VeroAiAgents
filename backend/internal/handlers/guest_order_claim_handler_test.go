package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/middlewares"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// HTTP-level tests for POST /api/v1/orders/claim — the explicit retry of the
// guest-order claim (GO-P1-3). The service-level rules are pinned in
// internal/services/guest_order_claim_test.go; what this file proves is that the
// endpoint exposes them without loosening anything:
//
//   - the request body is empty and no order id / email is ever accepted, so
//     neither can be used to select or move an order;
//   - the account comes from the auth context (Bearer token in production) and
//     the guest order from the HttpOnly cookie — both are required;
//   - each outcome maps to a distinct status: 200 claimed, 200 idempotent
//     replay (transferred=false), 404 nothing to claim, 409 owned by another
//     account, 401 no authenticated account.
//
// Harness note: SQLite in-memory with MaxOpenConns(1) (same as the service
// suite). It serializes the concurrency test through one connection, so
// `SELECT ... FOR UPDATE` is not exercised as it would be on Postgres; the
// marker-before-write logic that holds on either engine is.

// guestClaimCookieName mirrors the unexported auth.guestIdentityCookieName —
// the cookie the browser sends back on the claim request.
const guestClaimCookieName = "vero_guest_session"

// testUserHeader stands in for a verified Bearer token: the router below copies
// it into the same gin context key middlewares.Auth sets. Keeping JWT parsing
// out of these tests leaves exactly one variable under test — the claim.
const testUserHeader = "X-Test-User-Id"

type claimEnv struct {
	db   *gorm.DB
	repo *repositories.Repository
	svc  *services.Services
	h    *Handler
}

func setupClaimEnv(t *testing.T) claimEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("test database handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.GuestSession{}, &models.GuestOrderEntitlement{},
		&models.ChatSession{}, &models.Trip{}, &models.Itinerary{}, &models.Booking{}, &models.Payment{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	repo := repositories.New(db)
	// nil JWT service: the claim path never issues or verifies tokens, and no
	// AI key is needed (empty config → local fallback client). TTLs are set so
	// sessions minted through the real services are live, not instantly expired.
	svc := services.New(config.Config{
		GuestSessionTTL:  7 * 24 * time.Hour,
		GuestIdentityTTL: 30 * 24 * time.Hour,
	}, repo, nil, events.NewBus())
	t.Cleanup(svc.StopAudit)
	return claimEnv{db: db, repo: repo, svc: svc, h: &Handler{Services: svc}}
}

// seedGuestIdentity creates the throwaway guest user plus the guest session the
// raw token resolves to (production mints one per visitor).
func (e claimEnv) seedGuestIdentity(t *testing.T, token string) (models.User, models.GuestSession) {
	t.Helper()
	user := models.User{Name: "Guest Traveler", Email: "guest-" + uuid.NewString() + "@vero.local", Password: "x", Role: models.RoleUser}
	if err := e.db.Create(&user).Error; err != nil {
		t.Fatalf("create guest user: %v", err)
	}
	guest := models.GuestSession{TokenHash: services.HashGuestToken(token), UserID: user.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := e.db.Create(&guest).Error; err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	return user, guest
}

func (e claimEnv) seedTrip(t *testing.T) models.Trip {
	t.Helper()
	start := time.Now().Add(24 * time.Hour)
	end := time.Now().Add(72 * time.Hour)
	trip := models.Trip{Title: "Bali", Slug: "bali-" + uuid.NewString(), Status: "published",
		BasePrice: 1000, AdultPax: 10, ChildPax: 5, PackageStartDate: &start, PackageEndDate: &end}
	if err := e.db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}
	return trip
}

// seedAccount creates the real (loginable) account a claim targets. The email is
// explicit so the email-only attack test can mint an account that shares the
// order's contact address.
func (e claimEnv) seedAccount(t *testing.T, email string) models.User {
	t.Helper()
	account := models.User{Name: "Account", Email: email, Password: "x", Role: models.RoleUser}
	if err := e.db.Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	return account
}

// createGuestOrder places the single order a guest identity is allowed. Contact
// anchors stay distinct per order so the unrelated one-order-per-contact rule
// (GO-P0-1) never fires instead of the claim logic under test.
func (e claimEnv) createGuestOrder(t *testing.T, guestUser models.User, guest models.GuestSession, trip models.Trip, seq int, email string) models.Booking {
	t.Helper()
	if email == "" {
		email = fmt.Sprintf("guest%05d@example.com", seq)
	}
	req := dto.BookingRequest{
		TripID:       trip.ID,
		AdultPax:     1,
		ContactName:  "Guest",
		ContactEmail: email,
		ContactPhone: fmt.Sprintf("081200%05d", seq),
		TravelDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
	}
	booking, err := e.svc.Bookings.CreateGuest(context.Background(), guestUser.ID, guest.ID,
		fmt.Sprintf("claim-http-key-%08d", seq), req)
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	return booking
}

func (e claimEnv) reloadBooking(t *testing.T, id uuid.UUID) models.Booking {
	t.Helper()
	var booking models.Booking
	if err := e.db.First(&booking, "id = ?", id).Error; err != nil {
		t.Fatalf("reload booking: %v", err)
	}
	return booking
}

func (e claimEnv) reloadGuest(t *testing.T, id uuid.UUID) models.GuestSession {
	t.Helper()
	var guest models.GuestSession
	if err := e.db.First(&guest, "id = ?", id).Error; err != nil {
		t.Fatalf("reload guest session: %v", err)
	}
	return guest
}

type claimEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		OrderID     string `json:"order_id"`
		Transferred bool   `json:"transferred"`
	} `json:"data"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// newClaimRouter mounts the endpoint behind a stand-in for middlewares.Auth
// (same context key, no JWT). An absent header means "no authenticated
// account" — what an anonymous guest hitting the route looks like.
func newClaimRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/orders/claim", func(c *gin.Context) {
		if raw := c.GetHeader(testUserHeader); raw != "" {
			if id, err := uuid.Parse(raw); err == nil {
				c.Set(middlewares.ContextUserID, id)
			}
		}
	}, h.ClaimOrderToAccount)
	return r
}

// postClaim sends the real request shape: empty body, guest identity in the
// HttpOnly cookie, account from the auth context. No order id is transmitted
// anywhere — the endpoint accepts none.
func postClaim(t *testing.T, r *gin.Engine, userID uuid.UUID, guestToken string) (int, claimEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/claim", nil)
	if userID != uuid.Nil {
		req.Header.Set(testUserHeader, userID.String())
	}
	if guestToken != "" {
		req.AddCookie(&http.Cookie{Name: guestClaimCookieName, Value: guestToken})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env claimEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		// Errorf, not Fatalf: this helper also runs inside the goroutines of the
		// concurrency test, where FailNow would be called on the wrong goroutine.
		t.Errorf("decode envelope: %v (body=%s)", err, w.Body.String())
	}
	return w.Code, env
}

// assertUntouched fails when a refused claim moved anything: the booking must
// still be guest-owned and the guest session must carry no claim marker.
func (e claimEnv) assertUntouched(t *testing.T, bookingID, guestID, guestUserID uuid.UUID) {
	t.Helper()
	booking := e.reloadBooking(t, bookingID)
	if booking.GuestSessionID == nil || *booking.GuestSessionID != guestID {
		t.Fatalf("refused claim released the guest binding: %v", booking.GuestSessionID)
	}
	if booking.UserID != guestUserID {
		t.Fatalf("refused claim changed the owner: %s", booking.UserID)
	}
	if marker := e.reloadGuest(t, guestID); marker.ClaimedUserID != nil {
		t.Fatalf("refused claim recorded a claimant: %v", marker.ClaimedUserID)
	}
}

// TestClaimOrderEndpoint_ValidClaim: cookie + authenticated account moves the
// order once, records the claimant, and swaps the access path from guest to
// account.
func TestClaimOrderEndpoint_ValidClaim(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 1, "")
	account := e.seedAccount(t, "owner-"+uuid.NewString()+"@example.com")
	r := newClaimRouter(e.h)

	status, env := postClaim(t, r, account.ID, "guest-token")
	if status != http.StatusOK || !env.Success {
		t.Fatalf("claim should succeed: status=%d env=%+v", status, env)
	}
	if !env.Data.Transferred || env.Data.OrderID != booking.ID.String() {
		t.Fatalf("claim payload wrong: %+v", env.Data)
	}

	fresh := e.reloadBooking(t, booking.ID)
	if fresh.UserID != account.ID || fresh.GuestSessionID != nil {
		t.Fatalf("ownership not transferred: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	marker := e.reloadGuest(t, guest.ID)
	if marker.ClaimedUserID == nil || *marker.ClaimedUserID != account.ID || marker.ClaimedAt == nil {
		t.Fatalf("claim marker not recorded: user=%v at=%v", marker.ClaimedUserID, marker.ClaimedAt)
	}
	ctx := context.Background()
	if _, err := e.svc.Bookings.Find(ctx, booking.ID, account.ID, false); err != nil {
		t.Fatalf("account must reach the claimed order: %v", err)
	}
	// The guest path closes in the same statement that transferred ownership.
	if _, err := e.svc.Bookings.FindGuest(ctx, booking.ID, guest.ID); err == nil {
		t.Fatal("guest cookie must no longer reach the claimed order")
	}
}

// TestClaimOrderEndpoint_InvalidGuestIdentity: a missing cookie, a forged token,
// the stored hash presented as a token, and an expired session all answer 404
// with the same code — no transfer, and nothing that distinguishes "wrong guess"
// from "no order here".
func TestClaimOrderEndpoint_InvalidGuestIdentity(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 2, "")
	account := e.seedAccount(t, "nobody-"+uuid.NewString()+"@example.com")
	r := newClaimRouter(e.h)

	for _, token := range []string{"", "not-a-real-token", services.HashGuestToken("guest-token")} {
		status, env := postClaim(t, r, account.ID, token)
		if status != http.StatusNotFound || env.Error.Code != "NO_GUEST_ORDER_TO_CLAIM" {
			t.Fatalf("token %q: status=%d env=%+v, want 404 NO_GUEST_ORDER_TO_CLAIM", token, status, env)
		}
		e.assertUntouched(t, booking.ID, guest.ID, guestUser.ID)
	}

	// Genuine token, dead session: expiry alone is enough to refuse.
	if err := e.db.Model(&models.GuestSession{}).Where("id = ?", guest.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire guest session: %v", err)
	}
	status, env := postClaim(t, r, account.ID, "guest-token")
	if status != http.StatusNotFound {
		t.Fatalf("expired session: status=%d env=%+v, want 404", status, env)
	}
	e.assertUntouched(t, booking.ID, guest.ID, guestUser.ID)
}

// TestClaimOrderEndpoint_RequiresAuthenticatedAccount: holding the guest cookie
// is not enough — a claim needs an account. The handler fails closed on its own
// (401) so mounting the route without middlewares.Auth cannot write uuid.Nil
// into bookings.user_id.
func TestClaimOrderEndpoint_RequiresAuthenticatedAccount(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 3, "")
	r := newClaimRouter(e.h)

	status, env := postClaim(t, r, uuid.Nil, "guest-token")
	if status != http.StatusUnauthorized || env.Success {
		t.Fatalf("unauthenticated claim: status=%d env=%+v, want 401", status, env)
	}
	e.assertUntouched(t, booking.ID, guest.ID, guestUser.ID)
}

// TestClaimOrderEndpoint_WrongGuest: guest B cannot claim guest A's order even
// when B's own session row points at it (first_order_id forced to A's booking —
// the only way to even attempt this, since the endpoint accepts no order id).
// The UPDATE still requires the booking to reference the SAME guest session, so
// a pointer is not ownership. B's own order remains claimable afterwards: the
// refusal is targeted, not a lockout.
func TestClaimOrderEndpoint_WrongGuest(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUserA, guestA := e.seedGuestIdentity(t, "guest-token-a")
	guestUserB, guestB := e.seedGuestIdentity(t, "guest-token-b")
	bookingA := e.createGuestOrder(t, guestUserA, guestA, trip, 4, "")
	accountB := e.seedAccount(t, "b-"+uuid.NewString()+"@example.com")
	r := newClaimRouter(e.h)

	// B has no order yet: nothing to claim, A untouched.
	if status, env := postClaim(t, r, accountB.ID, "guest-token-b"); status != http.StatusNotFound {
		t.Fatalf("guest without an order: status=%d env=%+v, want 404", status, env)
	}
	e.assertUntouched(t, bookingA.ID, guestA.ID, guestUserA.ID)

	if err := e.db.Model(&models.GuestSession{}).Where("id = ?", guestB.ID).
		Update("first_order_id", bookingA.ID).Error; err != nil {
		t.Fatalf("point guest B at booking A: %v", err)
	}
	status, env := postClaim(t, r, accountB.ID, "guest-token-b")
	if status != http.StatusNotFound {
		t.Fatalf("cross-guest claim: status=%d env=%+v, want 404", status, env)
	}
	e.assertUntouched(t, bookingA.ID, guestA.ID, guestUserA.ID)
	if marker := e.reloadGuest(t, guestB.ID); marker.ClaimedUserID != nil {
		t.Fatalf("refused cross-guest claim recorded a claimant: %v", marker.ClaimedUserID)
	}

	bookingB := e.createGuestOrder(t, guestUserB, guestB, trip, 5, "")
	status, env = postClaim(t, r, accountB.ID, "guest-token-b")
	if status != http.StatusOK || !env.Data.Transferred || env.Data.OrderID != bookingB.ID.String() {
		t.Fatalf("guest B should claim its own order: status=%d env=%+v", status, env)
	}
	e.assertUntouched(t, bookingA.ID, guestA.ID, guestUserA.ID)
}

// TestClaimOrderEndpoint_WrongAuthenticatedUser: a second account behind the
// same guest cookie (shared browser, stolen cookie) is refused with 409. The
// order stays with its first claimant, the marker is not overwritten, and the
// refused account gains no read access.
func TestClaimOrderEndpoint_WrongAuthenticatedUser(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 6, "")
	accountA := e.seedAccount(t, "first-"+uuid.NewString()+"@example.com")
	accountB := e.seedAccount(t, "second-"+uuid.NewString()+"@example.com")
	r := newClaimRouter(e.h)

	if status, _ := postClaim(t, r, accountA.ID, "guest-token"); status != http.StatusOK {
		t.Fatalf("first claim status=%d, want 200", status)
	}

	status, env := postClaim(t, r, accountB.ID, "guest-token")
	if status != http.StatusConflict || env.Error.Code != "GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT" {
		t.Fatalf("second account: status=%d env=%+v, want 409 GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT", status, env)
	}
	if env.Data.Transferred {
		t.Fatal("refused claim reported a transfer")
	}

	fresh := e.reloadBooking(t, booking.ID)
	if fresh.UserID != accountA.ID || fresh.GuestSessionID != nil {
		t.Fatalf("order left its first owner: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	if marker := e.reloadGuest(t, guest.ID); marker.ClaimedUserID == nil || *marker.ClaimedUserID != accountA.ID {
		t.Fatalf("claim marker overwritten: %v", marker.ClaimedUserID)
	}
	if _, err := e.svc.Bookings.Find(context.Background(), booking.ID, accountB.ID, false); err == nil {
		t.Fatal("refused account must not read the order")
	}
}

// TestClaimOrderEndpoint_DuplicateClaimIsIdempotent: replays by the rightful
// account answer 200 with transferred=false and write nothing, so a
// double-submit or a client retry loop is safe.
func TestClaimOrderEndpoint_DuplicateClaimIsIdempotent(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 7, "")
	account := e.seedAccount(t, "idem-"+uuid.NewString()+"@example.com")
	r := newClaimRouter(e.h)

	status, env := postClaim(t, r, account.ID, "guest-token")
	if status != http.StatusOK || !env.Data.Transferred {
		t.Fatalf("first claim: status=%d env=%+v", status, env)
	}
	first := e.reloadGuest(t, guest.ID)

	for attempt := 0; attempt < 3; attempt++ {
		status, env := postClaim(t, r, account.ID, "guest-token")
		if status != http.StatusOK || !env.Success {
			t.Fatalf("replay %d: status=%d env=%+v, want 200", attempt, status, env)
		}
		if env.Data.Transferred {
			t.Fatalf("replay %d transferred again", attempt)
		}
		if env.Data.OrderID != booking.ID.String() {
			t.Fatalf("replay %d reported another order: %s", attempt, env.Data.OrderID)
		}
	}

	fresh := e.reloadBooking(t, booking.ID)
	if fresh.UserID != account.ID || fresh.GuestSessionID != nil {
		t.Fatalf("replays disturbed ownership: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	after := e.reloadGuest(t, guest.ID)
	if after.ClaimedUserID == nil || *after.ClaimedUserID != account.ID {
		t.Fatalf("claim marker changed: %v", after.ClaimedUserID)
	}
	if first.ClaimedAt == nil || after.ClaimedAt == nil || !after.ClaimedAt.Equal(*first.ClaimedAt) {
		t.Fatalf("replay rewrote claimed_at: %v -> %v", first.ClaimedAt, after.ClaimedAt)
	}
	var owned int64
	e.db.Model(&models.Booking{}).Where("user_id = ?", account.ID).Count(&owned)
	if owned != 1 {
		t.Fatalf("replays produced %d owned bookings", owned)
	}
}

// TestClaimOrderEndpoint_ConcurrentClaims: several accounts hitting the endpoint
// at the same time behind one guest cookie. Exactly one request transfers
// ownership; the rest are idempotent replays by the winner or 409 refusals, and
// the stored owner agrees with the winner.
func TestClaimOrderEndpoint_ConcurrentClaims(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 8, "")
	r := newClaimRouter(e.h)

	accounts := make([]models.User, 4)
	for i := range accounts {
		accounts[i] = e.seedAccount(t, fmt.Sprintf("race%d-%s@example.com", i, uuid.NewString()))
	}

	type outcome struct {
		userID uuid.UUID
		status int
		env    claimEnvelope
	}
	const attemptsPerAccount = 2
	var (
		mu       sync.Mutex
		outcomes []outcome
		wg       sync.WaitGroup
	)
	for _, account := range accounts {
		for attempt := 0; attempt < attemptsPerAccount; attempt++ {
			wg.Add(1)
			go func(userID uuid.UUID) {
				defer wg.Done()
				status, env := postClaim(t, r, userID, "guest-token")
				mu.Lock()
				outcomes = append(outcomes, outcome{userID: userID, status: status, env: env})
				mu.Unlock()
			}(account.ID)
		}
	}
	wg.Wait()

	transfers := 0
	winner := uuid.Nil
	for _, o := range outcomes {
		switch {
		case o.status == http.StatusOK && o.env.Data.Transferred:
			transfers++
			winner = o.userID
		case o.status == http.StatusOK:
			// Replay by the winning account: allowed, changes nothing.
		case o.status == http.StatusConflict:
			// Refused: another account already owns the order.
		default:
			t.Fatalf("unexpected claim outcome: status=%d env=%+v", o.status, o.env)
		}
	}
	if transfers != 1 {
		t.Fatalf("expected exactly one ownership transfer, got %d", transfers)
	}
	for _, o := range outcomes {
		if o.status == http.StatusOK && o.userID != winner {
			t.Fatalf("account %s got a success it did not win", o.userID)
		}
	}

	fresh := e.reloadBooking(t, booking.ID)
	if fresh.UserID != winner || fresh.GuestSessionID != nil {
		t.Fatalf("stored owner disagrees with the winner: user=%s guest=%v winner=%s", fresh.UserID, fresh.GuestSessionID, winner)
	}
	if marker := e.reloadGuest(t, guest.ID); marker.ClaimedUserID == nil || *marker.ClaimedUserID != winner {
		t.Fatalf("claim marker disagrees with the winner: %v", marker.ClaimedUserID)
	}
	var stillGuest int64
	e.db.Model(&models.Booking{}).Where("guest_session_id = ?", guest.ID).Count(&stillGuest)
	if stillGuest != 0 {
		t.Fatalf("%d bookings still guest-owned after the race", stillGuest)
	}
}

// TestClaimOrderEndpoint_AlreadyClaimedOrder: an order that already left the
// guest path without a claim marker (claimed before the marker columns existed,
// or moved out-of-band) is not re-decided. Its current owner replays with 200,
// any other account gets 409 — the marker backfill is a convenience, not the
// security boundary.
func TestClaimOrderEndpoint_AlreadyClaimedOrder(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	booking := e.createGuestOrder(t, guestUser, guest, trip, 9, "")
	owner := e.seedAccount(t, "legacy-"+uuid.NewString()+"@example.com")
	attacker := e.seedAccount(t, "legacy-attacker-"+uuid.NewString()+"@example.com")
	r := newClaimRouter(e.h)

	// Legacy state: ownership already transferred, no claim marker recorded.
	if err := e.db.Model(&models.Booking{}).Where("id = ?", booking.ID).
		Updates(map[string]interface{}{"user_id": owner.ID, "guest_session_id": nil}).Error; err != nil {
		t.Fatalf("simulate pre-migration claim: %v", err)
	}

	status, env := postClaim(t, r, owner.ID, "guest-token")
	if status != http.StatusOK || env.Data.Transferred || env.Data.OrderID != booking.ID.String() {
		t.Fatalf("rightful owner replay: status=%d env=%+v, want 200 transferred=false", status, env)
	}

	status, env = postClaim(t, r, attacker.ID, "guest-token")
	if status != http.StatusConflict || env.Error.Code != "GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT" {
		t.Fatalf("other account: status=%d env=%+v, want 409", status, env)
	}
	if fresh := e.reloadBooking(t, booking.ID); fresh.UserID != owner.ID {
		t.Fatalf("legacy order changed hands: %s", fresh.UserID)
	}
}

// TestClaimOrderEndpoint_EmailOnlyAttack: an account registered with the order's
// contact email — the whole point of "email equality is not proof" — claims
// nothing. Without the guest cookie every attempt is 404, the order stays with
// the guest, and the attacker cannot read it either. The rightful cookie holder
// still claims it afterwards.
func TestClaimOrderEndpoint_EmailOnlyAttack(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	guestUser, guest := e.seedGuestIdentity(t, "guest-token")
	const victimEmail = "victim@example.com"
	booking := e.createGuestOrder(t, guestUser, guest, trip, 10, victimEmail)
	attacker := e.seedAccount(t, victimEmail)
	r := newClaimRouter(e.h)

	// No cookie, a forged token, and a random UUID as a token: same 404.
	for _, token := range []string{"", "guest-token-forged", uuid.NewString()} {
		status, env := postClaim(t, r, attacker.ID, token)
		if status != http.StatusNotFound || env.Error.Code != "NO_GUEST_ORDER_TO_CLAIM" {
			t.Fatalf("email match must not enable a claim (token %q): status=%d env=%+v", token, status, env)
		}
		e.assertUntouched(t, booking.ID, guest.ID, guestUser.ID)
	}
	// Knowing the order id grants no read access either.
	if _, err := e.svc.Bookings.Find(context.Background(), booking.ID, attacker.ID, false); err == nil {
		t.Fatal("email match must not grant order access")
	}

	legit := e.seedAccount(t, "legit-"+uuid.NewString()+"@example.com")
	status, env := postClaim(t, r, legit.ID, "guest-token")
	if status != http.StatusOK || !env.Data.Transferred {
		t.Fatalf("cookie holder should claim the order: status=%d env=%+v", status, env)
	}
	if fresh := e.reloadBooking(t, booking.ID); fresh.UserID != legit.ID {
		t.Fatalf("order went to the wrong account: %s", fresh.UserID)
	}
}
