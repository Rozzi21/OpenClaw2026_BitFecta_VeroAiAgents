package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/handlers"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/routes"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// Route-wiring test for the guest-order claim retry endpoint
// (POST /api/v1/orders/claim, GO-P1-3). It runs against the REAL route table
// with a real JWT service and NO database — what is pinned here is the guard,
// not the claim logic (that lives in internal/handlers and internal/services
// tests):
//
//   - routes.Register must not panic: POST /api/v1/orders and
//     POST /api/v1/orders/claim coexisting in one method tree would surface as a
//     gin route conflict at startup.
//   - the endpoint decides ownership, so it must be unreachable without a valid
//     ACCESS token — missing, malformed, and refresh-audience tokens are all
//     rejected before the handler runs.

func newRouteTestEngine(t *testing.T) (*gin.Engine, *auth.JWTService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		JWTSecret:     "route-test-secret",
		JWTAccessTTL:  15 * time.Minute,
		JWTRefreshTTL: time.Hour,
	}
	jwt := auth.NewJWTService(cfg)
	// nil repository: no route touched by this test reaches the database. Google
	// OAuth stays disabled by the empty config, so no discovery/network happens.
	svc := services.New(cfg, nil, jwt, events.NewBus())
	t.Cleanup(svc.StopAudit)
	r := gin.New()
	routes.Register(r, handlers.New(svc, nil), svc)
	return r, jwt
}

func postClaimRoute(t *testing.T, r *gin.Engine, bearer string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/claim", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	return w.Code, envelope.Error.Code
}

func TestClaimOrderRouteRequiresAccessToken(t *testing.T) {
	r, jwt := newRouteTestEngine(t)
	user := models.User{BaseModel: models.BaseModel{ID: uuid.New()}, Email: "claimer@example.com", Role: models.RoleUser}
	pair, err := jwt.Generate(user)
	if err != nil {
		t.Fatalf("generate tokens: %v", err)
	}

	// No token, garbage token, and a REFRESH token presented as an access token:
	// all 401, none reach the claim.
	for name, bearer := range map[string]string{
		"missing": "",
		"garbage": "not-a-jwt",
		"refresh": pair.RefreshToken,
	} {
		if status, _ := postClaimRoute(t, r, bearer); status != http.StatusUnauthorized {
			t.Fatalf("%s token: status=%d, want 401", name, status)
		}
	}

	// Valid access token, no guest cookie: the handler runs and reports "nothing
	// to claim" (ClaimOrder short-circuits on the empty token, so this stays
	// database-free). The envelope code also proves the route exists — a missing
	// route would be a bare 404 with no code.
	status, code := postClaimRoute(t, r, pair.AccessToken)
	if status != http.StatusNotFound || code != "NO_GUEST_ORDER_TO_CLAIM" {
		t.Fatalf("authenticated claim without guest cookie: status=%d code=%q, want 404 NO_GUEST_ORDER_TO_CLAIM", status, code)
	}
}
