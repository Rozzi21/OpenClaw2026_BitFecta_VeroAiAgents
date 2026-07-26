package services_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	err = db.AutoMigrate(&models.User{}, &models.Trip{}, &models.Booking{}, &models.Payment{})
	if err != nil {
		t.Fatalf("Failed to migrate test database: %v", err)
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
		BookingID: booking.ID, 
		ExternalID: "EXT-123", 
		Amount: 1000, 
		Status: "pending",
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
		Status: "paid",
		Signature: sig,
		Timestamp: timestamp,
		RawBody: []byte(rawBody),
		Amount: &amount,
	}
	
	_, err := paymentSvc.Webhook(req)
	if err != nil {
		t.Errorf("Expected valid webhook to succeed, got: %v", err)
	}
	
	// Test 2: Expired timestamp (replay attack simulation)
	expiredTime := now.Add(-10 * time.Minute)
	expiredTimestamp := expiredTime.Format(time.RFC3339)
	sigExpired := generateDokuSignature(cfg.DOKUSecret, expiredTimestamp, rawBody)
	
	reqExpired := dto.PaymentWebhookRequest{
		ExternalID: "EXT-123",
		Status: "paid",
		Signature: sigExpired,
		Timestamp: expiredTimestamp,
		RawBody: []byte(rawBody),
		Amount: &amount,
	}
	
	_, err = paymentSvc.Webhook(reqExpired)
	if err == nil || err.Error() != "webhook timestamp expired" {
		t.Errorf("Expected webhook timestamp expired error, got: %v", err)
	}
	
	// Test 3: Invalid signature
	reqInvalidSig := dto.PaymentWebhookRequest{
		ExternalID: "EXT-123",
		Status: "paid",
		Signature: "invalid_sig",
		Timestamp: timestamp,
		RawBody: []byte(rawBody),
		Amount: &amount,
	}
	
	_, err = paymentSvc.Webhook(reqInvalidSig)
	if err == nil || err.Error() != "invalid payment signature" {
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
		BookingID: booking.ID, 
		ExternalID: "EXT-456", 
		Amount: 1000, 
		Status: "paid", // Already paid
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
		Status: "pending", // Attempt to downgrade to pending
		Signature: sig,
		Timestamp: timestamp,
		RawBody: []byte(rawBody),
		Amount: &amount,
	}
	
	_, err := paymentSvc.Webhook(req)
	if err == nil || err.Error() != "payment already settled" {
		t.Errorf("Expected payment already settled error, got: %v", err)
	}
}