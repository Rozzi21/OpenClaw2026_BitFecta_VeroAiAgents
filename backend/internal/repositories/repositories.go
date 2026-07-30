package repositories

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/gorm"
)

type RepositoryFilter struct {
	Limit  int
	Offset int
}

type TripRepositoryFilter struct {
	Category      string
	Status        string
	Search        string
	PublishedOnly bool
	Limit         int
	Offset        int
}

type Repository struct {
	DB *gorm.DB
}

func New(db *gorm.DB) *Repository {
	return &Repository{DB: db}
}

func (r *Repository) CreateUser(user *models.User) error {
	return r.DB.Create(user).Error
}

func (r *Repository) FindUserByEmail(email string) (models.User, error) {
	var user models.User
	err := r.DB.Where("email = ?", email).First(&user).Error
	return user, err
}

func (r *Repository) FirstOrCreateUser(user *models.User) error {
	return r.DB.Where("email = ?", user.Email).FirstOrCreate(user).Error
}

func (r *Repository) FindUserByID(id uuid.UUID) (models.User, error) {
	var user models.User
	err := r.DB.First(&user, "id = ?", id).Error
	return user, err
}

func (r *Repository) CreateChatSession(session *models.ChatSession) error {
	return r.DB.Create(session).Error
}

func (r *Repository) FindChatSession(id uuid.UUID) (models.ChatSession, error) {
	var session models.ChatSession
	err := r.DB.First(&session, "id = ?", id).Error
	return session, err
}

func (r *Repository) UpdateChatSession(session *models.ChatSession) error {
	return r.DB.Save(session).Error
}

func (r *Repository) UpdateChatSessionMemorySummary(sessionID uuid.UUID, summary string) error {
	return r.DB.Model(&models.ChatSession{}).Where("id = ?", sessionID).Update("memory_summary", summary).Error
}

func (r *Repository) UpdateChatSessionSelectedTrip(sessionID uuid.UUID, tripID *uuid.UUID) error {
	return r.DB.Model(&models.ChatSession{}).Where("id = ?", sessionID).Update("selected_trip_id", tripID).Error
}

func (r *Repository) UpdateChatSessionActivity(sessionID uuid.UUID, expiresAt, lastActivityAt time.Time) error {
	return r.DB.Model(&models.ChatSession{}).Where("id = ?", sessionID).Updates(map[string]interface{}{
		"expires_at":       expiresAt,
		"last_activity_at": lastActivityAt,
	}).Error
}

func (r *Repository) FindBookingBySession(sessionID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.Order("created_at desc").First(&booking, "session_id = ?", sessionID).Error
	return booking, err
}

func (r *Repository) ListChatSessions(userID uuid.UUID) ([]models.ChatSession, error) {
	var sessions []models.ChatSession
	err := r.DB.Where("user_id = ?", userID).Order("created_at desc").Find(&sessions).Error
	return sessions, err
}

func (r *Repository) DeleteExpiredChatSessions(before time.Time) (int64, error) {
	// SEC-19: Must delete child records (chat_messages, tool_calls, ai_logs)
	// before deleting the session, otherwise they become orphans in DB.
	tx := r.DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}

	var sessions []models.ChatSession
	if err := tx.Select("id").Where("expires_at IS NOT NULL AND expires_at < ?", before).Find(&sessions).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if len(sessions) == 0 {
		tx.Rollback()
		return 0, nil
	}

	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.ID.String())
	}

	// Unscoped() is used to hard delete the orphans, since keeping soft deleted
	// records for anonymous guest sessions just wastes space.
	if err := tx.Unscoped().Where("session_id IN ?", ids).Delete(&models.ChatMessage{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Unscoped().Where("session_id IN ?", ids).Delete(&models.ToolCall{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Unscoped().Where("session_id IN ?", ids).Delete(&models.AILog{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	result := tx.Where("id IN ?", ids).Delete(&models.ChatSession{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

func (r *Repository) CountExpiredChatSessions(before time.Time) (int64, error) {
	var count int64
	err := r.DB.Model(&models.ChatSession{}).Where("expires_at IS NOT NULL AND expires_at < ?", before).Count(&count).Error
	return count, err
}

func (r *Repository) AddChatMessage(message *models.ChatMessage) error {
	return r.DB.Create(message).Error
}

func (r *Repository) ListChatMessages(sessionID uuid.UUID) ([]models.ChatMessage, error) {
	var messages []models.ChatMessage
	err := r.DB.Where("session_id = ?", sessionID).Order("created_at asc").Find(&messages).Error
	return messages, err
}

func (r *Repository) ListRecentChatMessages(sessionID uuid.UUID, limit int) ([]models.ChatMessage, error) {
	var newest []models.ChatMessage
	if limit <= 0 {
		limit = 8
	}
	if err := r.DB.Where("session_id = ?", sessionID).Order("created_at desc").Limit(limit).Find(&newest).Error; err != nil {
		return nil, err
	}
	messages := make([]models.ChatMessage, len(newest))
	for i := range newest {
		messages[len(newest)-1-i] = newest[i]
	}
	return messages, nil
}

func (r *Repository) CountChatMessages(sessionID uuid.UUID) (int64, error) {
	var count int64
	err := r.DB.Model(&models.ChatMessage{}).Where("session_id = ?", sessionID).Count(&count).Error
	return count, err
}

func (r *Repository) CreateTrip(trip *models.Trip) error {
	return r.DB.Create(trip).Error
}

func (r *Repository) ListTrips(query TripRepositoryFilter) ([]models.Trip, error) {
	var trips []models.Trip
	db := r.DB.Preload("Itineraries").Order("created_at desc")
	if query.Category != "" {
		db = db.Where("category = ?", strings.ToLower(query.Category))
	}
	if query.Status != "" {
		db = db.Where("status = ?", strings.ToLower(query.Status))
	}
	if query.PublishedOnly {
		db = db.Where("status = ?", "published")
	}
	if query.Search != "" {
		like := "%" + strings.ToLower(query.Search) + "%"
		db = db.Where("LOWER(title) LIKE ? OR LOWER(destination) LIKE ? OR LOWER(location) LIKE ?", like, like, like)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}
	if query.Offset > 0 {
		db = db.Offset(query.Offset)
	}
	err := db.Find(&trips).Error
	return trips, err
}

func (r *Repository) FindTrip(id uuid.UUID) (models.Trip, error) {
	var trip models.Trip
	err := r.DB.Preload("Itineraries").First(&trip, "id = ?", id).Error
	return trip, err
}

func (r *Repository) FindTripBySlugOrID(value string) (models.Trip, error) {
	var trip models.Trip
	if id, err := uuid.Parse(value); err == nil {
		err = r.DB.Preload("Itineraries").First(&trip, "id = ?", id).Error
		return trip, err
	}
	err := r.DB.Preload("Itineraries").First(&trip, "slug = ?", value).Error
	return trip, err
}

func (r *Repository) UpdateTrip(trip *models.Trip) error {
	return r.DB.Save(trip).Error
}

func (r *Repository) ReplaceTripItineraries(tripID uuid.UUID, itineraries []models.Itinerary) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trip_id = ?", tripID).Delete(&models.Itinerary{}).Error; err != nil {
			return err
		}
		if len(itineraries) == 0 {
			return nil
		}
		for i := range itineraries {
			itineraries[i].TripID = tripID
		}
		return tx.Create(&itineraries).Error
	})
}

func (r *Repository) DeleteTrip(id uuid.UUID) error {
	return r.DB.Delete(&models.Trip{}, "id = ?", id).Error
}

func (r *Repository) CreateBooking(booking *models.Booking) error {
	return r.DB.Create(booking).Error
}

func (r *Repository) ListBookings(query RepositoryFilter) ([]models.Booking, error) {
	var bookings []models.Booking
	err := r.DB.Preload("User").Preload("Trip").Preload("Payments").
		Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&bookings).Error
	return bookings, err
}

// RecentBookings returns the most recent bookings (without payments preload) for
// analytics dashboards. This avoids the full-table scan + 3-preload pattern that
// ListBookings uses, keeping dashboard queries lightweight.
func (r *Repository) RecentBookings(limit int) ([]models.Booking, error) {
	if limit <= 0 {
		limit = 10
	}
	var bookings []models.Booking
	err := r.DB.Preload("User").Preload("Trip").
		Order("created_at desc").Limit(limit).Find(&bookings).Error
	return bookings, err
}

func (r *Repository) FindBooking(id uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.Preload("User").Preload("Trip").Preload("Payments").First(&booking, "id = ?", id).Error
	return booking, err
}

// FindBookingForUser scopes the lookup to a single owner (SEC-2 anti-IDOR).
// Staff callers should use FindBooking instead.
func (r *Repository) FindBookingForUser(id, userID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.Preload("User").Preload("Trip").Preload("Payments").
		First(&booking, "id = ? AND user_id = ?", id, userID).Error
	return booking, err
}

func (r *Repository) CreatePayment(payment *models.Payment) error {
	return r.DB.Create(payment).Error
}

func (r *Repository) FindPayment(id uuid.UUID) (models.Payment, error) {
	var payment models.Payment
	err := r.DB.Preload("Booking").First(&payment, "id = ?", id).Error
	return payment, err
}

// FindPaymentForUser scopes the lookup to the owner of the related booking
// (SEC-2 anti-IDOR). Staff callers should use FindPayment instead.
func (r *Repository) FindPaymentForUser(id, userID uuid.UUID) (models.Payment, error) {
	var payment models.Payment
	err := r.DB.Preload("Booking").
		Joins("JOIN bookings ON bookings.id = payments.booking_id").
		Where("payments.id = ? AND bookings.user_id = ?", id, userID).
		First(&payment).Error
	return payment, err
}

func (r *Repository) FindPaymentByExternalID(externalID string) (models.Payment, error) {
	var payment models.Payment
	err := r.DB.Preload("Booking").First(&payment, "external_id = ?", externalID).Error
	return payment, err
}

func (r *Repository) UpdatePayment(payment *models.Payment) error {
	return r.DB.Save(payment).Error
}

func (r *Repository) UpdateBooking(booking *models.Booking) error {
	return r.DB.Save(booking).Error
}

// UpdateBookingStatusAtomic performs an atomic conditional update of the booking
// status. It only succeeds if the current DB status matches fromStatus, preventing
// TOCTOU race conditions (SEC-23). Returns true if the row was updated (race won),
// false if the status had already changed (race lost).
func (r *Repository) UpdateBookingStatusAtomic(id uuid.UUID, fromStatus, toStatus string) (bool, error) {
	result := r.DB.Model(&models.Booking{}).
		Where("id = ? AND booking_status = ?", id, fromStatus).
		Update("booking_status", toStatus)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (r *Repository) CreateAILog(log *models.AILog) error {
	return r.DB.Create(log).Error
}

func (r *Repository) ListAILogs(query RepositoryFilter) ([]models.AILog, error) {
	var logs []models.AILog
	err := r.DB.Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&logs).Error
	return logs, err
}

func (r *Repository) CreateToolCall(call *models.ToolCall) error {
	return r.DB.Create(call).Error
}

func (r *Repository) ListToolCalls(query RepositoryFilter) ([]models.ToolCall, error) {
	var calls []models.ToolCall
	err := r.DB.Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&calls).Error
	return calls, err
}

// TailChatMessages returns the last N messages (oldest-first) for a chat session.
// Unlike ListChatMessages which loads ALL messages, this only fetches the tail,
// making it efficient for memory-summary refresh on long conversations.
func (r *Repository) TailChatMessages(sessionID uuid.UUID, limit int) ([]models.ChatMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	var newest []models.ChatMessage
	if err := r.DB.Where("session_id = ?", sessionID).Order("created_at desc").Limit(limit).Find(&newest).Error; err != nil {
		return nil, err
	}
	messages := make([]models.ChatMessage, len(newest))
	for i := range newest {
		messages[len(newest)-1-i] = newest[i]
	}
	return messages, nil
}
