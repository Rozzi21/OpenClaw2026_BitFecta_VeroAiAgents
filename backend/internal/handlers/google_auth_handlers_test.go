package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// Google OAuth handler tests (29 Agu 2026). The Google service is enabled with
// an OFFLINE client (no discovery, no network, no real credentials); the
// tested guard paths return before any token exchange.

func newGoogleOAuthTestHandler(t *testing.T) *Handler {
	t.Helper()
	cfg := config.Config{GoogleOAuthFrontendURL: "http://localhost:3000"}
	client, err := auth.NewGoogleClientOfflineForTest("cid", "secret", "http://localhost/cb")
	if err != nil {
		t.Fatalf("offline google client: %v", err)
	}
	// repo/issuer are nil: the guard paths under test never reach them.
	googleSvc := services.NewGoogleOAuthServiceForTest(cfg, nil, nil, client)
	return &Handler{Services: &services.Services{Config: cfg, Google: googleSvc}}
}

func serveCallback(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newGoogleOAuthTestHandler(t)
	r.GET("/callback", h.GoogleCallback)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	r.ServeHTTP(w, req)
	return w
}

// TestGoogleCallback_MissingAuthorizationCode: a callback without `code`
// (and/or `state`) must bounce the user to the frontend login screen with the
// generic missing_params error — never panicking, never exchanging.
func TestGoogleCallback_MissingAuthorizationCode(t *testing.T) {
	for _, target := range []string{
		"/callback",               // both missing
		"/callback?state=abc",     // code missing
		"/callback?code=authcode", // state missing
	} {
		w := serveCallback(t, target)
		if w.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want 302", target, w.Code)
		}
		loc := w.Header().Get("Location")
		if !strings.HasPrefix(loc, "http://localhost:3000/login?") {
			t.Errorf("%s: redirect = %q, want frontend /login", target, loc)
		}
		if !strings.Contains(loc, "auth_error=missing_params") {
			t.Errorf("%s: redirect missing auth_error=missing_params: %q", target, loc)
		}
	}
}

// TestGoogleCallback_ProviderErrorParam: Google bouncing the user back with
// ?error=access_denied must redirect with the generic access_denied code.
func TestGoogleCallback_ProviderErrorParam(t *testing.T) {
	w := serveCallback(t, "/callback?error=access_denied&state=abc&code=x")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "auth_error=access_denied") {
		t.Errorf("redirect missing auth_error=access_denied: %q", loc)
	}
}

// TestGoogleCallback_DisabledIs404: with the feature flag off the endpoint
// behaves as an unregistered route (404), not a redirect that leaks config.
func TestGoogleCallback_DisabledIs404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.Config{GoogleOAuthFrontendURL: "http://localhost:3000"}
	// Disabled service: no client injected → Enabled() false.
	googleSvc := services.NewGoogleOAuthServiceForTest(cfg, nil, nil, nil)
	h := &Handler{Services: &services.Services{Config: cfg, Google: googleSvc}}
	r.GET("/callback", h.GoogleCallback)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/callback?code=x&state=y", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when feature disabled", w.Code)
	}
}
