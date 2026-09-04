package services_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Real-engine (PostgreSQL) verification of the guest-order concurrency
// guarantees. The default SQLite harness runs on ONE connection, so
// `SELECT ... FOR UPDATE` is never contended and an aborted transaction never
// happens (GO-P3-6). These tests do the same scenarios with a real pool, where
// row locks, predicate re-evaluation after the lock, and constraint-aborted
// transactions actually occur.
//
// Skipped unless VERO_TEST_POSTGRES_DSN is set, so `go test ./...` stays
// database-free:
//
//	VERO_TEST_POSTGRES_DSN="host=127.0.0.1 user=… password=… dbname=… \
//	  search_path=toctou_verify_tmp sslmode=disable" go test -run TestPostgres ./internal/services/
//
// The tests TRUNCATE the tables they use, so the DSN must isolate them in a
// throwaway schema whose name says so — never the application schema.

func dsnSearchPath(dsn string) string {
	for _, field := range strings.Fields(dsn) {
		if value, ok := strings.CutPrefix(field, "search_path="); ok {
			return value
		}
	}
	return ""
}

// isPlainSQLIdentifier keeps the schema name safe to interpolate into DDL (it
// cannot be a bind parameter). Anything outside [A-Za-z0-9_] is rejected.
func isPlainSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

func setupPostgresRaceDB(t *testing.T) *repositories.Repository {
	t.Helper()
	dsn := os.Getenv("VERO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("VERO_TEST_POSTGRES_DSN not set: skipping real-engine concurrency verification")
	}
	schema := dsnSearchPath(dsn)
	if schema == "" || (!strings.Contains(schema, "test") && !strings.Contains(schema, "verify")) {
		t.Fatal(`refusing to run: VERO_TEST_POSTGRES_DSN must set search_path to a throwaway schema whose name contains "test" or "verify" (these tests TRUNCATE)`)
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("postgres handle: %v", err)
	}
	// Several connections: the point of these tests is real contention.
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Self-provision the throwaway schema so the run is reproducible. The name
	// already had to contain test/verify; re-check it is a plain identifier
	// before interpolating it (it cannot be a bind parameter in DDL).
	if !isPlainSQLIdentifier(schema) {
		t.Fatalf("search_path schema %q must be a plain identifier ([A-Za-z0-9_])", schema)
	}
	if err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + schema).Error; err != nil {
		t.Fatalf("create throwaway schema: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.GuestSession{}, &models.GuestOrderEntitlement{},
		&models.ChatSession{}, &models.ChatMessage{}, &models.Trip{}, &models.Itinerary{},
		&models.Booking{}, &models.Payment{}); err != nil {
		t.Fatalf("migrate postgres test schema: %v", err)
	}
	if err := db.Exec(`TRUNCATE TABLE payments, bookings, guest_order_entitlements,
		chat_messages, chat_sessions, guest_sessions, itineraries, trips, users CASCADE`).Error; err != nil {
		t.Fatalf("reset postgres test schema: %v", err)
	}
	return repositories.New(db)
}

// TestPostgresConcurrentGuestOrdersCreateOnlyOne: N parallel orders from ONE
// guest identity with DIFFERENT idempotency keys. Here `SELECT ... FOR UPDATE`
// on the guest row is genuinely contended: one request consumes the allowance,
// every other one must be refused after Postgres re-evaluates the row it waited
// for.
func TestPostgresConcurrentGuestOrdersCreateOnlyOne(t *testing.T) {
	repo := setupPostgresRaceDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const attempts = 6
	var (
		mu      sync.Mutex
		created []uuid.UUID
		refused int
		unknown []error
		wg      sync.WaitGroup
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			booking, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID,
				fmt.Sprintf("pg-race-key-%08d", i),
				contactBookingReq(trip, fmt.Sprintf("pg%d@example.com", i), fmt.Sprintf("0812990%04d", i)))
			mu.Lock()
			switch {
			case err == nil:
				created = append(created, booking.ID)
			case errors.Is(err, services.ErrGuestOrderLimitReached):
				refused++
			default:
				unknown = append(unknown, err)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(unknown) > 0 {
		t.Fatalf("unexpected error: %v", unknown[0])
	}
	if len(created) != 1 {
		t.Fatalf("expected exactly one guest order, got %d (refused=%d)", len(created), refused)
	}
	if got := countRows(t, repo.DB, &models.Booking{}, ""); got != 1 {
		t.Fatalf("expected one booking row, got %d", got)
	}
	fresh := reloadGuestSession(t, repo.DB, guest.ID)
	if fresh.OrderCount != 1 || fresh.FirstOrderID == nil || *fresh.FirstOrderID != created[0] {
		t.Fatalf("allowance state wrong: count=%d first=%v", fresh.OrderCount, fresh.FirstOrderID)
	}
}

// TestPostgresConcurrentSameKeyReplaysWinner: N parallel orders with the SAME
// owner + key. On Postgres the unique index aborts the losers' transactions, so
// this is the path the SQLite harness cannot reach: every loser must still come
// back with the winner's booking instead of a constraint error.
func TestPostgresConcurrentSameKeyReplaysWinner(t *testing.T) {
	repo := setupPostgresRaceDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const attempts = 6
	const key = "pg-same-key-00000001"
	var (
		mu       sync.Mutex
		ids      []uuid.UUID
		failures []error
		wg       sync.WaitGroup
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			booking, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID, key,
				contactBookingReq(trip, "pg-same@example.com", "081299000"))
			mu.Lock()
			if err != nil {
				failures = append(failures, err)
			} else {
				ids = append(ids, booking.ID)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("same-key race must always replay, got %d failures: %v", len(failures), failures[0])
	}
	if len(ids) != attempts {
		t.Fatalf("expected every caller to receive a booking, got %d/%d", len(ids), attempts)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("same key produced different bookings: %s vs %s", ids[0], id)
		}
	}
	if got := countRows(t, repo.DB, &models.Booking{}, ""); got != 1 {
		t.Fatalf("expected one booking row, got %d", got)
	}
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 2 {
		t.Fatalf("expected the contact anchors exactly once, got %d", got)
	}
}

// TestPostgresConcurrentClaimsTransferOnce: several accounts claim the same guest
// order at once. With a real pool the claim's `FOR UPDATE` on the guest row is
// contended, and Postgres re-checks the predicate on the version it waited for —
// so the loser sees the marker the winner wrote and is refused.
func TestPostgresConcurrentClaimsTransferOnce(t *testing.T) {
	repo := setupPostgresRaceDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "pg-claim-key-000001", claimBookingReq(trip, 31))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	accounts := make([]models.User, 4)
	for i := range accounts {
		accounts[i] = seedClaimAccount(t, repo.DB, fmt.Sprintf("pg-race%d-%s@example.com", i, uuid.NewString()))
	}

	var (
		mu        sync.Mutex
		transfers []uuid.UUID
		replays   int
		conflicts int
		unknown   []error
		wg        sync.WaitGroup
	)
	for _, account := range accounts {
		for attempt := 0; attempt < 2; attempt++ {
			wg.Add(1)
			go func(userID uuid.UUID) {
				defer wg.Done()
				result, err := svc.Guests.ClaimOrder(ctx, "guest-token", userID)
				mu.Lock()
				switch {
				case err == nil && result.Transferred:
					transfers = append(transfers, userID)
				case err == nil:
					replays++
				case errors.Is(err, services.ErrGuestOrderClaimConflict):
					conflicts++
				default:
					unknown = append(unknown, err)
				}
				mu.Unlock()
			}(account.ID)
		}
	}
	wg.Wait()

	if len(unknown) > 0 {
		t.Fatalf("unexpected claim error: %v", unknown[0])
	}
	if len(transfers) != 1 {
		t.Fatalf("expected exactly one transfer, got %d (replays=%d conflicts=%d)", len(transfers), replays, conflicts)
	}
	fresh := reloadBooking(t, repo.DB, booking.ID)
	if fresh.UserID != transfers[0] || fresh.GuestSessionID != nil {
		t.Fatalf("stored owner disagrees with the winner: user=%s guest=%v winner=%s", fresh.UserID, fresh.GuestSessionID, transfers[0])
	}
	marker := reloadGuestSession(t, repo.DB, guest.ID)
	if marker.ClaimedUserID == nil || *marker.ClaimedUserID != transfers[0] {
		t.Fatalf("claim marker disagrees with the winner: %v", marker.ClaimedUserID)
	}
}

// TestPostgresConcurrentChatBindSingleWinner: the conditional chat→guest bind
// (correlated NOT EXISTS on the row being updated) under real contention —
// exactly one identity may own the chat session, and the stored value agrees.
func TestPostgresConcurrentChatBindSingleWinner(t *testing.T) {
	repo := setupPostgresRaceDB(t)
	svc := newGuestClaimServices(repo)
	chat := seedChatSession(t, repo.DB)
	ctx := context.Background()

	identities := make([]models.GuestSession, 5)
	for i := range identities {
		_, identities[i] = newGuestIdentity(t, repo.DB, fmt.Sprintf("pg-bind-%d", i))
	}

	var (
		mu      sync.Mutex
		winners []uuid.UUID
		unknown []error
		wg      sync.WaitGroup
	)
	for _, identity := range identities {
		wg.Add(1)
		go func(guestID uuid.UUID) {
			defer wg.Done()
			err := svc.Guests.AttachChat(ctx, chat.ID, guestID)
			mu.Lock()
			switch {
			case err == nil:
				winners = append(winners, guestID)
			case errors.Is(err, services.ErrChatSessionGuestMismatch):
			default:
				unknown = append(unknown, err)
			}
			mu.Unlock()
		}(identity.ID)
	}
	wg.Wait()

	if len(unknown) > 0 {
		t.Fatalf("unexpected bind error: %v", unknown[0])
	}
	if len(winners) != 1 {
		t.Fatalf("expected exactly one winning bind, got %d", len(winners))
	}
	bound := reloadChatSession(t, repo.DB, chat.ID)
	if bound.GuestSessionID == nil || *bound.GuestSessionID != winners[0] {
		t.Fatalf("stored binding disagrees with the winner: %v want %s", bound.GuestSessionID, winners[0])
	}
}

// TestPostgresConcurrentIdentityResolutionIsStable: concurrent requests with the
// same live guest cookie resolve to the same identity — no second allowance.
func TestPostgresConcurrentIdentityResolutionIsStable(t *testing.T) {
	repo := setupPostgresRaceDB(t)
	_, _, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	var (
		mu      sync.Mutex
		ids     []uuid.UUID
		unknown []error
		wg      sync.WaitGroup
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, err := svc.Guests.Resolve(ctx, "guest-token")
			mu.Lock()
			if err != nil {
				unknown = append(unknown, err)
			} else {
				ids = append(ids, identity.Session.ID)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(unknown) > 0 {
		t.Fatalf("resolve must not fail: %v", unknown[0])
	}
	for _, id := range ids {
		if id != guest.ID {
			t.Fatalf("resolved a different identity: %s want %s", id, guest.ID)
		}
	}
	if got := countRows(t, repo.DB, &models.GuestSession{}, ""); got != 1 {
		t.Fatalf("expected one guest session, got %d", got)
	}
}
