package handlers

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// Google OAuth handlers (18 Agu 2026). Unlike the JSON auth endpoints these
// are full-page navigations, so they answer with HTTP redirects, not the
// JSON envelope: /google 302s to the Google consent screen and
// /google/callback 302s back to the frontend. The access token is delivered
// in the URL FRAGMENT (#...) of the final redirect so it never reaches access
// logs or the backend as a query param; the refresh token goes out as the
// usual HttpOnly cookie on the 302 response. The link flow (/google/link →
// /google/link/callback) shares GoogleCallback; the OAuth state's
// link_user_id selects the linking branch.

func (h *Handler) GoogleLogin(c *gin.Context) {
	if !h.Services.Google.Enabled() {
		utils_NotFoundOAuth(c)
		return
	}
	result, err := h.Services.Google.StartLogin(c.Request.Context(), c.Query("return_to"), nil)
	if err != nil {
		log.Printf("[google-login] start failed: %v", err)
		h.redirectOAuthError(c, "/", "start_failed")
		return
	}
	c.Redirect(http.StatusFound, result.RedirectURL)
}

// GoogleLinkStart begins the explicit "Link Google Account" flow for an
// ALREADY AUTHENTICATED user (Account Settings → Link Google). It reuses the
// same Google consent redirect but stamps the OAuth state with the caller's
// user ID, so the callback links the verified Google identity to that account
// instead of resolving/creating one. Requires the Auth middleware (route
// guard) — the user must prove ownership of the Vero account first.
func (h *Handler) GoogleLinkStart(c *gin.Context) {
	if !h.Services.Google.Enabled() {
		utils_NotFoundOAuth(c)
		return
	}
	uid := currentUserID(c)
	if uid == uuid.Nil {
		h.redirectOAuthError(c, "/", "unauthorized")
		return
	}
	result, err := h.Services.Google.StartLogin(c.Request.Context(), c.Query("return_to"), &uid)
	if err != nil {
		log.Printf("[google-link] start failed user=%s: %v", uid, err)
		h.redirectOAuthError(c, "/", "start_failed")
		return
	}
	c.Redirect(http.StatusFound, result.RedirectURL)
}

func (h *Handler) GoogleCallback(c *gin.Context) {
	cfg := h.Services.Config
	if !h.Services.Google.Enabled() {
		utils_NotFoundOAuth(c)
		return
	}

	// Google can bounce the user back with an error (e.g. access_denied).
	if gerr := c.Query("error"); gerr != "" {
		h.redirectOAuthError(c, "/", "access_denied")
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		h.redirectOAuthError(c, "/", "missing_params")
		return
	}

	result, err := h.Services.Google.Callback(c.Request.Context(), code, state, authRequestMeta(c))
	if err != nil {
		// SEC-15: raw error stays server-side; the client gets a generic code.
		log.Printf("[google-callback] failed: %v", err)
		code := "authentication_failed"
		if errors.Is(err, services.ErrGoogleAccountExists) {
			code = "account_exists_link_required"
		} else if errors.Is(err, services.ErrGoogleIdentityTaken) {
			code = "google_identity_taken"
		}
		h.redirectOAuthError(c, "/", code)
		return
	}

	// Link flow: the Google identity was linked to an already-authenticated
	// account. No new session is issued (the user already has one) — just send
	// them back with a success marker.
	if linkedID := result.LinkedUserID(); linkedID != "" {
		returnTo := result.ReturnTo
		sep := "?"
		if strings.Contains(returnTo, "?") {
			sep = "&"
		}
		target := strings.TrimRight(cfg.GoogleOAuthFrontendURL, "/") + returnTo + sep + "google_linked=1"
		c.Redirect(http.StatusFound, target)
		return
	}

	// Claim any guest order to the now-authenticated account, mirroring the
	// password login/register handlers.
	if user, ok := result.Issue.Response.User.(models.User); ok {
		if err := h.Services.Guests.ClaimOrder(c.Request.Context(), auth.GetGuestIdentityCookie(c), user.ID); err != nil {
			log.Printf("[google-callback] guest order claim failed user=%s: %v", user.ID, err)
		}
	}

	// Issue the normal Vero session: refresh cookie on the 302 response,
	// access token in the fragment of the redirect target.
	auth.SetRefreshCookie(c, cfg, result.Issue.RefreshToken, int(cfg.JWTRefreshTTL.Seconds()))

	returnTo := result.ReturnTo
	sep := "#"
	frag := url.Values{}
	frag.Set("access_token", result.Issue.Response.AccessToken)
	frag.Set("token_type", "Bearer")
	frag.Set("expires_in", strconv.FormatInt(result.Issue.Response.ExpiresIn, 10))
	frag.Set("provider", "google")
	target := strings.TrimRight(cfg.GoogleOAuthFrontendURL, "/") + returnTo + sep + frag.Encode()
	c.Redirect(http.StatusFound, target)
}

// redirectOAuthError sends the user back to the frontend login screen with a
// generic, log-safe error code (never the raw internal error — SEC-15).
func (h *Handler) redirectOAuthError(c *gin.Context, returnTo, code string) {
	base := strings.TrimRight(h.Services.Config.GoogleOAuthFrontendURL, "/")
	path := returnTo
	if path == "" || path == "/" {
		path = "/login"
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	c.Redirect(http.StatusFound, base+path+sep+"auth_error="+url.QueryEscape(code))
}

// utils_NotFoundOAuth hides the feature when disabled (same as an unregistered
// route) so the surface area does not leak configuration.
func utils_NotFoundOAuth(c *gin.Context) {
	c.Status(http.StatusNotFound)
}
