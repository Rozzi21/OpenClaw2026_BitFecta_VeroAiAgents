package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/database"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/middlewares"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

type Handler struct {
	Services  *services.Services
	Database  *database.Database
	StartedAt time.Time
}

func New(s *services.Services, db *database.Database) *Handler {
	return &Handler{Services: s, Database: db, StartedAt: time.Now()}
}

func (h *Handler) Health(c *gin.Context) {
	utils.Success(c, http.StatusOK, "Service healthy", gin.H{
		"service": "vero-travel-api",
		"status":  "healthy",
		"uptime":  time.Since(h.StartedAt).String(),
	})
}

func (h *Handler) DatabaseHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	// SEC-15: do not leak raw DB errors (DSN fragments, connection details) on a
	// public endpoint; log server-side instead.
	if err := h.Database.Health(ctx); err != nil {
		log.Printf("[health] database check failed: %v", err)
		utils.Error(c, http.StatusServiceUnavailable, "Database disconnected", gin.H{})
		return
	}
	utils.Success(c, http.StatusOK, "Database connected", gin.H{"database": "connected"})
}

func (h *Handler) Register(c *gin.Context) {
	var req dto.RegisterRequest
	if !bind(c, &req) {
		return
	}
	result, err := h.Services.Auth.Register(req, authRequestMeta(c))
	if err != nil {
		// SEC-15: hide raw service/DB errors (e.g. duplicate-email constraint)
		// from the client; log server-side.
		log.Printf("[register] failed: %v", err)
		utils.BadRequest(c, "Registration failed", gin.H{})
		return
	}
	respondAuthIssue(c, h.Services.Config, http.StatusCreated, "Registered", result)
}

func (h *Handler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if !bind(c, &req) {
		return
	}
	result, err := h.Services.Auth.Login(req, authRequestMeta(c))
	if err != nil {
		utils.Unauthorized(c, err.Error())
		return
	}
	respondAuthIssue(c, h.Services.Config, http.StatusOK, "Logged in", result)
}

func (h *Handler) Refresh(c *gin.Context) {
	refreshToken := auth.GetRefreshCookie(c, h.Services.Config)
	result, err := h.Services.Auth.Refresh(refreshToken, authRequestMeta(c))
	if err != nil {
		message := "Invalid refresh token"
		if errors.Is(err, services.ErrRefreshTokenRevoked) {
			message = "Refresh token revoked"
		}
		utils.Unauthorized(c, message)
		return
	}
	result.Response.User = nil
	respondAuthIssue(c, h.Services.Config, http.StatusOK, "Token refreshed", result)
}

func (h *Handler) Logout(c *gin.Context) {
	refreshToken := auth.GetRefreshCookie(c, h.Services.Config)
	_ = h.Services.Auth.Logout(refreshToken, authRequestMeta(c))
	auth.ClearRefreshCookie(c, h.Services.Config)
	utils.Success(c, http.StatusOK, "Logged out", gin.H{})
}

func (h *Handler) Me(c *gin.Context) {
	user, err := h.Services.Auth.Me(currentUserID(c))
	if err != nil {
		utils.NotFound(c, "User not found")
		return
	}
	utils.Success(c, http.StatusOK, "Current user", user)
}

// AdminCreateUser provisions operator/admin accounts. Guarded by Role(admin).
func (h *Handler) AdminCreateUser(c *gin.Context) {
	var req dto.AdminCreateUserRequest
	if !bind(c, &req) {
		return
	}
	user, err := h.Services.Auth.CreateStaff(req, authRequestMeta(c))
	if err != nil {
		log.Printf("[admin-create-user] failed: %v", err)
		utils.BadRequest(c, "Create user failed", gin.H{})
		return
	}
	utils.Success(c, http.StatusCreated, "User created", user)
}

func (h *Handler) Chat(c *gin.Context) {
	var req dto.ChatRequest
	if !bind(c, &req) {
		return
	}
	if req.SessionID == nil {
		utils.BadRequest(c, "session_id is required", gin.H{})
		return
	}
	userID := currentUserID(c)
	res, err := h.Services.AI.Chat(services.ChatContext{SessionID: *req.SessionID, UserID: &userID}, req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "AI workflow completed", res)
}

func (h *Handler) GuestChat(c *gin.Context) {
	var req dto.ChatRequest
	if !bind(c, &req) {
		return
	}
	sessionID, err := resolveGuestSession(h, c)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	res, err := h.Services.AI.Chat(services.ChatContext{SessionID: sessionID}, req)
	if err != nil {
		if err.Error() == "chat session expired" || err.Error() == "chat session not found" {
			auth.ClearGuestSessionCookie(c, h.Services.Config)
			utils.BadRequest(c, "Chat session expired", gin.H{})
			return
		}
		utils.ServerError(c, err)
		return
	}
	auth.SetGuestSessionCookie(c, h.Services.Config, sessionID.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
	utils.Success(c, http.StatusOK, "AI workflow completed", res)
}

func (h *Handler) ChatSessions(c *gin.Context) {
	sessions, err := h.Services.Repo.ListChatSessions(currentUserID(c))
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Chat sessions", sessions)
}

func (h *Handler) ChatMessages(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	session, err := h.Services.Repo.FindChatSession(id)
	if err != nil || session.UserID == nil || *session.UserID != currentUserID(c) || (session.ExpiresAt != nil && !session.ExpiresAt.After(time.Now())) {
		utils.NotFound(c, "Chat session not found")
		return
	}
	messages, err := h.Services.Repo.ListChatMessages(id)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Chat messages", messages)
}

// GuestHistory restores the current anonymous session without accepting an
// identifier from the request. The cookie is the ownership proof.
func (h *Handler) GuestHistory(c *gin.Context) {
	id, err := uuid.Parse(auth.GetGuestSessionCookie(c))
	if err != nil {
		utils.Success(c, http.StatusOK, "Chat history", gin.H{"messages": []models.ChatMessage{}})
		return
	}
	session, err := h.Services.Repo.FindChatSession(id)
	if err != nil || session.UserID != nil || (session.ExpiresAt != nil && !session.ExpiresAt.After(time.Now())) {
		auth.ClearGuestSessionCookie(c, h.Services.Config)
		utils.Success(c, http.StatusOK, "Chat history", gin.H{"messages": []models.ChatMessage{}})
		return
	}
	now := time.Now()
	expiresAt := now.Add(h.Services.Config.GuestSessionTTL)
	if err := h.Services.Repo.UpdateChatSessionActivity(id, expiresAt, now); err != nil {
		utils.ServerError(c, err)
		return
	}
	messages, err := h.Services.Repo.ListChatMessages(id)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	// Do not return ChatMessage.SessionID. The HttpOnly cookie is the only
	// guest-session identifier and remains inaccessible to JavaScript.
	guestMessages := make([]gin.H, 0, len(messages))
	for _, message := range messages {
		guestMessages = append(guestMessages, gin.H{"role": message.Role, "content": message.Content})
	}
	auth.SetGuestSessionCookie(c, h.Services.Config, id.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
	utils.Success(c, http.StatusOK, "Chat history", gin.H{"messages": guestMessages})
}

func resolveGuestSession(h *Handler, c *gin.Context) (uuid.UUID, error) {
	if cookieID := auth.GetGuestSessionCookie(c); cookieID != "" {
		id, err := uuid.Parse(cookieID)
		if err != nil {
			auth.ClearGuestSessionCookie(c, h.Services.Config)
		} else if session, err := h.Services.Repo.FindChatSession(id); err == nil && session.UserID == nil && (session.ExpiresAt == nil || session.ExpiresAt.After(time.Now())) {
			return id, nil
		}
	}
	now := time.Now()
	expiresAt := now.Add(h.Services.Config.GuestSessionTTL)
	session := models.ChatSession{Title: "Guest chat", ExpiresAt: &expiresAt, LastActivityAt: &now}
	if err := h.Services.Repo.CreateChatSession(&session); err != nil {
		return uuid.Nil, err
	}
	auth.SetGuestSessionCookie(c, h.Services.Config, session.ID.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
	return session.ID, nil
}

func (h *Handler) ListTrips(c *gin.Context) {
	var query dto.TripListQuery
	_ = c.ShouldBindQuery(&query)
	trips, err := h.Services.Trips.List(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Trips", trips)
}

func (h *Handler) PublicPackages(c *gin.Context) {
	var query dto.TripListQuery
	_ = c.ShouldBindQuery(&query)
	query.PublishedOnly = true
	trips, err := h.Services.Trips.List(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Packages", trips)
}

func (h *Handler) GetPackage(c *gin.Context) {
	trip, err := h.Services.Trips.FindBySlugOrID(c.Param("id"))
	if err != nil || trip.Status != "published" {
		utils.NotFound(c, "Package not found")
		return
	}
	utils.Success(c, http.StatusOK, "Package", trip)
}

func (h *Handler) GetTrip(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	trip, err := h.Services.Trips.Find(id)
	if err != nil {
		utils.NotFound(c, "Trip not found")
		return
	}
	utils.Success(c, http.StatusOK, "Trip", trip)
}

func (h *Handler) CreateTrip(c *gin.Context) {
	var req dto.TripRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.Services.Trips.Create(req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Trip created", trip)
}

func (h *Handler) UpdateTrip(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req dto.TripRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.Services.Trips.Update(id, req)
	if err != nil {
		utils.NotFound(c, "Trip not found")
		return
	}
	utils.Success(c, http.StatusOK, "Trip updated", trip)
}

func (h *Handler) DeleteTrip(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	if err := h.Services.Trips.Delete(id); err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Trip deleted", gin.H{"id": id})
}

func (h *Handler) CreateBooking(c *gin.Context) {
	var req dto.BookingRequest
	if !bind(c, &req) {
		return
	}
	booking, err := h.Services.Bookings.Create(currentUserID(c), req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Booking created", booking)
}

func (h *Handler) GuestCreateOrder(c *gin.Context) {
	var req dto.BookingRequest
	if !bind(c, &req) {
		return
	}
	user, err := h.Services.Auth.GuestUser()
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	booking, err := h.Services.Bookings.Create(user.ID, req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Order created for manual admin processing", booking)
}

func (h *Handler) ListBookings(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	bookings, err := h.Services.Bookings.List(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Bookings", bookings)
}

func (h *Handler) GetBooking(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	booking, err := h.Services.Bookings.Find(id, currentUserID(c), isStaff(c))
	if err != nil {
		utils.NotFound(c, "Booking not found")
		return
	}
	utils.Success(c, http.StatusOK, "Booking", booking)
}

func (h *Handler) UpdateBooking(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateBookingStatusRequest
	if !bind(c, &req) {
		return
	}
	booking, err := h.Services.Bookings.UpdateStatus(id, currentUserID(c), isStaff(c), req)
	if err != nil {
		if errors.Is(err, services.ErrBookingNotFound) {
			utils.NotFound(c, "Booking not found")
			return
		}
		utils.BadRequest(c, "Update booking failed", gin.H{})
		return
	}
	utils.Success(c, http.StatusOK, "Booking updated", booking)
}

func (h *Handler) CreatePayment(c *gin.Context) {
	if !h.Services.Config.PaymentsEnabled {
		h.PaymentFeatureDisabled(c)
		return
	}
	var req dto.PaymentCreateRequest
	if !bind(c, &req) {
		return
	}
	payment, err := h.Services.Payments.Create(req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Payment created", payment)
}

func (h *Handler) PaymentWebhook(c *gin.Context) {
	if !h.Services.Config.PaymentsEnabled {
		h.PaymentFeatureDisabled(c)
		return
	}
	var req dto.PaymentWebhookRequest
	if !bind(c, &req) {
		return
	}
	if req.Signature == "" {
		req.Signature = c.GetHeader("X-Doku-Signature")
	}
	if req.Timestamp == "" {
		req.Timestamp = c.GetHeader("X-Doku-Timestamp")
	}

	rawBody, err := c.GetRawData()
	if err == nil {
		req.RawBody = rawBody
	}

	payment, err := h.Services.Payments.Webhook(req)
	if err != nil {
		// SEC-15: do not echo internal/payment errors to an unauthenticated
		// caller; log server-side.
		log.Printf("[payment-webhook] rejected: %v", err)
		utils.BadRequest(c, "Webhook failed", gin.H{})
		return
	}
	utils.Success(c, http.StatusOK, "Payment updated", payment)
}

func (h *Handler) GetPayment(c *gin.Context) {
	if !h.Services.Config.PaymentsEnabled {
		h.PaymentFeatureDisabled(c)
		return
	}
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	payment, err := h.Services.Payments.Find(id, currentUserID(c), isStaff(c))
	if err != nil {
		utils.NotFound(c, "Payment not found")
		return
	}
	utils.Success(c, http.StatusOK, "Payment", payment)
}

func (h *Handler) PaymentFeatureDisabled(c *gin.Context) {
	utils.Error(c, http.StatusServiceUnavailable, "Payment feature temporarily disabled", gin.H{
		"detail": "DOKU payment flow is disabled; orders are saved as pending for manual backoffice processing.",
	})
}

func (h *Handler) Logs(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	logs, err := h.Services.Logs.Logs(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "AI logs", logs)
}

func (h *Handler) WorkflowLogs(c *gin.Context) {
	h.Logs(c)
}

func (h *Handler) ToolCalls(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	calls, err := h.Services.Logs.ToolCalls(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Tool calls", calls)
}

func (h *Handler) Analytics(c *gin.Context) {
	data, err := h.Services.Analytics.Dashboard()
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Analytics dashboard", data)
}

// maxUploadBytes caps a single media upload (SEC-5).
const maxUploadBytes = 5 << 20 // 5 MiB

func (h *Handler) UploadTripMedia(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		utils.BadRequest(c, "Upload failed: multipart file field 'file' is required", gin.H{})
		return
	}

	// SEC-5: enforce a size limit before touching disk.
	if file.Size <= 0 || file.Size > maxUploadBytes {
		utils.BadRequest(c, "File too large", gin.H{"max_bytes": maxUploadBytes, "size": file.Size})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true}
	if !allowed[ext] {
		utils.BadRequest(c, "Unsupported media type", gin.H{"extension": ext})
		return
	}

	// SEC-5: verify the real content type from the file's magic bytes instead of
	// trusting the filename extension alone.
	if detected, err := detectImageContentType(file); err != nil {
		log.Printf("[upload] unable to read file: %v", err)
		utils.BadRequest(c, "Unable to read file", gin.H{})
		return
	} else if !strings.HasPrefix(detected, "image/") {
		utils.BadRequest(c, "File content is not a valid image", gin.H{"detected": detected})
		return
	}

	dir := filepath.Join("uploads", "trips")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		utils.ServerError(c, err)
		return
	}
	filename := uuid.NewString() + ext
	path := filepath.Join(dir, filename)
	if err := c.SaveUploadedFile(file, path); err != nil {
		utils.ServerError(c, err)
		return
	}

	utils.Success(c, http.StatusCreated, "Media uploaded", dto.UploadResponse{
		URL:      "/" + filepath.ToSlash(path),
		Filename: filename,
		Size:     file.Size,
	})
}

// detectImageContentType sniffs the first 512 bytes to determine the true MIME
// type of an uploaded file (SEC-5).
func detectImageContentType(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

// sseMaxLifetime adalah umur maksimal satu koneksi SSE (BUG-4). Koneksi
// setengah-putus (client hilang tanpa FIN — NAT timeout, laptop sleep) tidak
// cepat memicu Context.Done dan write ke buffer TCP masih "berhasil", sehingga
// goroutine SSE bisa hidup lama. Setelah umur ini tercapai, handler menutup
// koneksi; client EventSource akan reconnect otomatis. 30 menit cukup untuk
// sesi monitoring backoffice tanpa menumpuk zombie.
const sseMaxLifetime = 30 * time.Minute

// sseHeartbeatInterval adalah interval heartbeat. Memakai time.NewTicker (bukan
// time.After) agar tidak ada timer leak (SEC-31).
const sseHeartbeatInterval = 25 * time.Second

func (h *Handler) EventStream(c *gin.Context) {
	// BUG-4: tolak koneksi baru bila bus sudah penuh (cap subscriber). Tanpa
	// ini, koneksi zombie menumpuk tak terbatas di map clients.
	client, ok := h.Services.Events.Subscribe()
	if !ok {
		utils.Error(c, http.StatusServiceUnavailable, "Too many SSE connections", gin.H{})
		return
	}
	defer h.Services.Events.Unsubscribe(client)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	// BUG-4: write-error detection. Pada koneksi setengah-putus, write ke buffer
	// TCP bisa tetap "berhasil" untuk sementara, tetapi setelah buffer penuh /
	// RST diterima, Flush/error akan terlihat. ResponseController.SetWriteDeadline
	// memberi deadline per-tulis; Write/Flush melewati deadline → error terdeteksi
	// → return false → goroutine keluar, subscriber dilepas. Ini menutup celah
	// goroutine zombie yang hidup berjam-jam menunggu RST OS.
	rc := http.NewResponseController(c.Writer)

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	deadline := time.NewTimer(sseMaxLifetime)
	defer deadline.Stop()

	send := func(typ string, event events.Event) bool {
		// Deadline per-write; write yang menggantung (client zombie) diputus.
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		c.SSEvent(typ, event)
		// Flush mendorong bytes ke socket; error di sini = koneksi mati.
		if err := rc.Flush(); err != nil {
			return false
		}
		return true
	}

	for {
		select {
		case event := <-client:
			if !send(event.Type, event) {
				return
			}
		case <-heartbeat.C:
			if !send("heartbeat", events.Event{ID: uuid.NewString(), Type: "heartbeat", CreatedAt: time.Now()}) {
				return
			}
		case <-c.Request.Context().Done():
			return
		case <-deadline.C:
			// BUG-4: umur maksimal tercapai. Kirim event close lalu tutup;
			// client EventSource reconnect otomatis.
			_ = send("reconnect", events.Event{ID: uuid.NewString(), Type: "reconnect", CreatedAt: time.Now()})
			return
		}
	}
}

func authRequestMeta(c *gin.Context) services.AuthRequestMeta {
	requestID, _ := c.Get("request_id")
	id, _ := requestID.(string)
	return services.AuthRequestMeta{
		IP:        c.ClientIP(),
		UserAgent: c.GetHeader("User-Agent"),
		RequestID: id,
	}
}

func respondAuthIssue(c *gin.Context, cfg config.Config, status int, message string, result services.AuthIssueResult) {
	maxAge := int(cfg.JWTRefreshTTL.Seconds())
	auth.SetRefreshCookie(c, cfg, result.RefreshToken, maxAge)
	utils.Success(c, status, message, result.Response)
}

func bind(c *gin.Context, target interface{}) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		utils.BadRequest(c, "Validation failed", gin.H{"detail": err.Error()})
		return false
	}
	return true
}

func parseID(c *gin.Context, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(key))
	if err != nil {
		utils.BadRequest(c, fmt.Sprintf("Invalid %s", key), gin.H{"detail": err.Error()})
		return uuid.Nil, false
	}
	return id, true
}

func currentUserID(c *gin.Context) uuid.UUID {
	value, exists := c.Get(middlewares.ContextUserID)
	if !exists {
		return uuid.Nil
	}
	id, ok := value.(uuid.UUID)
	if !ok {
		return uuid.Nil
	}
	return id
}

// isStaff reports whether the current request belongs to an operator/admin.
// Used for ownership checks: staff may read any resource, others only their own.
func isStaff(c *gin.Context) bool {
	value, exists := c.Get(middlewares.ContextRole)
	if !exists {
		return false
	}
	role, ok := value.(models.Role)
	if !ok {
		return false
	}
	return role == models.RoleOperator || role == models.RoleAdmin
}
