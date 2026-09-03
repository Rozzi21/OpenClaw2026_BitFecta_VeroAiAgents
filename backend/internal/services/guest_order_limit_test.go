package services_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupGuestTestDB(t *testing.T) (*gorm.DB, *repositories.Repository) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get test database handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	err = db.AutoMigrate(&models.User{}, &models.GuestSession{}, &models.GuestOrderEntitlement{}, &models.ChatSession{}, &models.Trip{}, &models.Itinerary{}, &models.Booking{}, &models.Payment{})
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	return db, repositories.New(db)
}

func seedGuestFixture(t *testing.T, db *gorm.DB) (models.User, models.Trip, models.GuestSession) {
	t.Helper()
	user := models.User{Name: "Guest Traveler", Email: "guest-" + uuid.NewString() + "@vero.local", Password: "x", Role: models.RoleUser}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	start := time.Now().Add(24 * time.Hour)
	end := time.Now().Add(72 * time.Hour)
	trip := models.Trip{Title: "Bali", Slug: "bali-" + uuid.NewString(), Status: "published", BasePrice: 1000, AdultPax: 10, ChildPax: 5, PackageStartDate: &start, PackageEndDate: &end}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}
	guest := models.GuestSession{TokenHash: services.HashGuestToken("guest-token"), UserID: user.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create guest: %v", err)
	}
	return user, trip, guest
}

func validBookingReq(trip models.Trip) dto.BookingRequest {
	return dto.BookingRequest{TripID: trip.ID, AdultPax: 1, ContactName: "Guest", ContactPhone: "0812", TravelDate: time.Now().Add(48 * time.Hour).Format("2006-01-02")}
}

func newGuestBookingService(repo *repositories.Repository) *services.BookingService {
	return services.New(config.Config{}, repo, nil, events.NewBus()).Bookings
}

func TestGuestFirstOrderSucceedsSecondFails(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	first, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000001", validBookingReq(trip))
	if err != nil {
		t.Fatalf("first guest order failed: %v", err)
	}
	if first.GuestSessionID == nil || *first.GuestSessionID != guest.ID {
		t.Fatal("booking should retain guest ownership")
	}
	var fresh models.GuestSession
	if err := repo.DB.First(&fresh, "id = ?", guest.ID).Error; err != nil {
		t.Fatalf("reload guest: %v", err)
	}
	if fresh.OrderCount != 1 || fresh.FirstOrderID == nil || *fresh.FirstOrderID != first.ID {
		t.Fatalf("entitlement not consumed atomically: count=%d first=%v", fresh.OrderCount, fresh.FirstOrderID)
	}

	_, err = svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000002", validBookingReq(trip))
	if !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("second guest order should hit limit, got %v", err)
	}
	var count int64
	repo.DB.Model(&models.Booking{}).Where("guest_session_id = ?", guest.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected one guest booking, got %d", count)
	}
}

func TestGuestOrderAccessOwnership(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	otherUser := models.User{Name: "Other", Email: "guest-" + uuid.NewString() + "@vero.local", Password: "x", Role: models.RoleUser}
	repo.DB.Create(&otherUser)
	otherGuest := models.GuestSession{TokenHash: services.HashGuestToken("other-token"), UserID: otherUser.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	repo.DB.Create(&otherGuest)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	booking, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000003", validBookingReq(trip))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.FindGuest(ctx, booking.ID, guest.ID); err != nil {
		t.Fatalf("owner should access order: %v", err)
	}
	if _, err := svc.FindGuest(ctx, booking.ID, otherGuest.ID); !errors.Is(err, services.ErrBookingNotFound) {
		t.Fatalf("other guest should not access order, got %v", err)
	}
	if _, err := svc.FindGuest(ctx, uuid.New(), guest.ID); !errors.Is(err, services.ErrBookingNotFound) {
		t.Fatalf("guessed UUID should not grant access, got %v", err)
	}
}

func TestFailedAttemptsDoNotConsumeGuestAllowance(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	badReq := validBookingReq(trip)
	badReq.ContactPhone = ""
	badReq.ContactEmail = ""
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000004", badReq); !errors.Is(err, services.ErrBookingContactRequired) {
		t.Fatalf("invalid contact should fail validation, got %v", err)
	}
	badReq = validBookingReq(trip)
	badReq.TravelDate = "not-a-date"
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000005", badReq); !errors.Is(err, services.ErrBookingTravelDateInvalid) {
		t.Fatalf("invalid date should fail validation, got %v", err)
	}
	badReq = validBookingReq(trip)
	badReq.TripID = uuid.New()
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000006", badReq); err == nil {
		t.Fatal("invalid trip should fail")
	}
	var fresh models.GuestSession
	repo.DB.First(&fresh, "id = ?", guest.ID)
	if fresh.OrderCount != 0 {
		t.Fatalf("failed attempts consumed allowance: count=%d", fresh.OrderCount)
	}
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000007", validBookingReq(trip)); err != nil {
		t.Fatalf("guest should still get first successful order: %v", err)
	}
}

func TestConcurrentGuestOrdersCreateOnlyOne(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()
	const workers = 8
	var successes atomic.Int64
	var limits atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateGuest(ctx, user.ID, guest.ID, fmt.Sprintf("idem-key-race-%03d", i), validBookingReq(trip))
			if err == nil {
				successes.Add(1)
				return
			}
			if errors.Is(err, services.ErrGuestOrderLimitReached) {
				limits.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("expected exactly one success, got %d (limits=%d)", successes.Load(), limits.Load())
	}
	var count int64
	repo.DB.Model(&models.Booking{}).Where("guest_session_id = ?", guest.ID).Count(&count)
	if count != 1 {
		t.Fatalf("race created %d bookings", count)
	}
}

func TestIdempotentRetryReturnsSameBooking(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()
	req := validBookingReq(trip)
	first, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000008", req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000008", req)
	if err != nil {
		t.Fatalf("retry should be idempotent after limit consumption: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry created duplicate booking: %s vs %s", first.ID, second.ID)
	}
	var count int64
	repo.DB.Model(&models.Booking{}).Where("guest_session_id = ?", guest.ID).Count(&count)
	if count != 1 {
		t.Fatalf("retry produced %d bookings", count)
	}
}

func TestAuthenticatedUserNotBlockedByGuestLimit(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()
	if _, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000009", validBookingReq(trip)); err != nil {
		t.Fatalf("guest first order: %v", err)
	}
	account := models.User{Name: "Account", Email: "acct-" + uuid.NewString() + "@example.com", Password: "x", Role: models.RoleUser}
	repo.DB.Create(&account)
	first, err := svc.Create(ctx, account.ID, "idem-key-0000000010", validBookingReq(trip))
	if err != nil {
		t.Fatalf("authenticated first order: %v", err)
	}
	second, err := svc.Create(ctx, account.ID, "idem-key-0000000011", validBookingReq(trip))
	if err != nil {
		t.Fatalf("authenticated second order should be allowed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("authenticated orders should be distinct")
	}
}

func TestGuestOrderClaimTransitionsToAccountOnce(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	user, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()
	booking, err := svc.CreateGuest(ctx, user.ID, guest.ID, "idem-key-0000000012", validBookingReq(trip))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	account := models.User{Name: "Account", Email: "acct-" + uuid.NewString() + "@example.com", Password: "x", Role: models.RoleUser}
	repo.DB.Create(&account)
	claimedID, err := repo.ClaimGuestOrder(ctx, guest.ID, account.ID)
	if err != nil {
		t.Fatalf("claim should succeed: %v", err)
	}
	if claimedID != booking.ID {
		t.Fatalf("claimed wrong booking: %s", claimedID)
	}
	var fresh models.Booking
	repo.DB.First(&fresh, "id = ?", booking.ID)
	if fresh.UserID != account.ID || fresh.GuestSessionID != nil {
		t.Fatalf("claim did not transfer ownership safely: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	if _, err := repo.ClaimGuestOrder(ctx, guest.ID, account.ID); err == nil {
		t.Fatal("second claim should fail because order was already claimed")
	}
	if _, err := svc.Find(ctx, booking.ID, account.ID, false); err != nil {
		t.Fatalf("account should access claimed order: %v", err)
	}
}
