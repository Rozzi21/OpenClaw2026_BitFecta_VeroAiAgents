package services_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, *repositories.Repository) {
	// Use a unique in-memory database per test. The shared DSN allowed tables and
	// rows from one test to survive into another and made parallel tests flaky.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get test database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	err = db.AutoMigrate(&models.User{}, &models.Trip{}, &models.Booking{}, &models.Payment{})
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	return db, repositories.New(db)
}

func generateDokuSignature(secret string, timestamp string, rawBody string) string {
	message := timestamp + "|" + rawBody
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestPaymentWebhookReplay(t *testing.T) {
	db, repo := setupTestDB(t)
	bus := events.NewBus()
	cfg := config.Config{PaymentsEnabled: true, DOKUSecret: "test_secret_123"}

	// Create mock booking & payment
	user := models.User{Name: "Test User", Email: "test@example.com", Role: models.RoleUser}
	db.Create(&user)

	trip := models.Trip{Title: "Test Trip", BasePrice: 1000}
	db.Create(&trip)

	booking := models.Booking{UserID: user.ID, TripID: trip.ID, TotalPrice: 1000, BookingStatus: "pending"}
	db.Create(&booking)

	payment := models.Payment{
		BookingID:  booking.ID,
		ExternalID: "EXT-123",
		Amount:     1000,
		Status:     "pending",
	}
	db.Create(&payment)

	paymentSvc := services.New(cfg, repo, nil, bus).Payments

	// Test 1: Valid signature, fresh timestamp
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339)
	rawBody := `{"external_id":"EXT-123","status":"paid"}`
	sig := generateDokuSignature(cfg.DOKUSecret, timestamp, rawBody)

	amount := float64(1000)
	req := dto.PaymentWebhookRequest{
		ExternalID: "EXT-123",
		Status:     "paid",
		Signature:  sig,
		Timestamp:  timestamp,
		RawBody:    []byte(rawBody),
		Amount:     &amount,
	}

	_, err := paymentSvc.Webhook(context.Background(), req)
	if err != nil {
		t.Errorf("Expected valid webhook to succeed, got: %v", err)
	}

	// Test 2: Expired timestamp (replay attack simulation)
	expiredTime := now.Add(-10 * time.Minute)
	expiredTimestamp := expiredTime.Format(time.RFC3339)
	sigExpired := generateDokuSignature(cfg.DOKUSecret, expiredTimestamp, rawBody)

	reqExpired := dto.PaymentWebhookRequest{
		ExternalID: "EXT-123",
		Status:     "paid",
		Signature:  sigExpired,
		Timestamp:  expiredTimestamp,
		RawBody:    []byte(rawBody),
		Amount:     &amount,
	}

	_, err = paymentSvc.Webhook(context.Background(), reqExpired)
	if !errors.Is(err, services.ErrWebhookTimestampExpired) {
		t.Errorf("Expected webhook timestamp expired error, got: %v", err)
	}

	// Test 3: Invalid signature
	reqInvalidSig := dto.PaymentWebhookRequest{
		ExternalID: "EXT-123",
		Status:     "paid",
		Signature:  "invalid_sig",
		Timestamp:  timestamp,
		RawBody:    []byte(rawBody),
		Amount:     &amount,
	}

	_, err = paymentSvc.Webhook(context.Background(), reqInvalidSig)
	if !errors.Is(err, services.ErrInvalidPaymentSignature) {
		t.Errorf("Expected invalid payment signature error, got: %v", err)
	}
}

func TestPaymentWebhookIdempotency(t *testing.T) {
	db, repo := setupTestDB(t)
	bus := events.NewBus()
	cfg := config.Config{PaymentsEnabled: true, DOKUSecret: "test_secret"}

	booking := models.Booking{TotalPrice: 1000, BookingStatus: "pending"}
	db.Create(&booking)

	payment := models.Payment{
		BookingID:  booking.ID,
		ExternalID: "EXT-456",
		Amount:     1000,
		Status:     "paid", // Already paid
	}
	db.Create(&payment)

	paymentSvc := services.New(cfg, repo, nil, bus).Payments

	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339)
	rawBody := `{"external_id":"EXT-456","status":"pending"}`
	sig := generateDokuSignature(cfg.DOKUSecret, timestamp, rawBody)

	amount := float64(1000)
	req := dto.PaymentWebhookRequest{
		ExternalID: "EXT-456",
		Status:     "pending", // Attempt to downgrade to pending
		Signature:  sig,
		Timestamp:  timestamp,
		RawBody:    []byte(rawBody),
		Amount:     &amount,
	}

	_, err := paymentSvc.Webhook(context.Background(), req)
	if !errors.Is(err, services.ErrPaymentAlreadySettled) {
		t.Errorf("Expected payment already settled error, got: %v", err)
	}
}
