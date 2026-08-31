// Customer access-token storage + OAuth fragment handling.
//
// Threat model (see docs/GOOGLE_OAUTH.md): the access token is a 15-minute
// Bearer JWT held in localStorage so it can be attached as an Authorization
// header; the long-lived refresh token stays in an HttpOnly cookie and is
// NEVER readable here. localStorage is NOT safe against XSS — these helpers
// only shrink the exposure window and blast radius:
//   - tokens are validated for JWT shape before storage AND before use, so a
//     crafted localStorage value or malicious URL fragment is never attached
//     as a Bearer header;
//   - tokens carry an expiry (from expires_in or the JWT exp claim) and are
//     treated as absent once expired, so dead tokens do not linger and every
//     expiry forces a refresh through the HttpOnly cookie;
//   - clearing removes BOTH the token and its expiry marker.
//
// This module is dependency-free and pure where possible so it is unit-testable
// with the Node test runner (no DOM needed beyond a localStorage stub).

const TOKEN_KEY = "vero_customer_access_token";
const TOKEN_EXPIRES_AT_KEY = "vero_customer_access_token_expires_at";

// Hard cap so an attacker-controlled fragment/localStorage value cannot make
// the app store or transmit an unbounded string. Real JWTs are << 8 KiB.
const MAX_TOKEN_LENGTH = 8192;

// Treat the token as expired slightly early so a request never races the
// backend's own expiry check.
const EXPIRY_SKEW_MS = 30_000;

const BASE64URL_SEGMENT = /^[A-Za-z0-9_-]+$/;

// isPlausibleAccessToken checks the compact-JWT shape (three non-empty
// base64url segments). This is NOT a signature check — the backend always
// verifies — it only stops garbage from being stored or sent.
export function isPlausibleAccessToken(token: string): boolean {
  if (!token || token.length > MAX_TOKEN_LENGTH) {
    return false;
  }
  const parts = token.split(".");
  if (parts.length !== 3) {
    return false;
  }
  return parts.every((part) => part.length > 0 && BASE64URL_SEGMENT.test(part));
}

function decodeBase64Url(segment: string): string {
  const b64 = segment.replace(/-/g, "+").replace(/_/g, "/");
  const padded = b64 + "=".repeat((4 - (b64.length % 4)) % 4);
  return atob(padded);
}

// tokenExpiryMs reads the JWT `exp` claim WITHOUT verifying the signature.
// It is only a client-side expiry hint (when to refresh); trust decisions are
// always made server-side. Returns null when unreadable.
export function tokenExpiryMs(token: string): number | null {
  if (!isPlausibleAccessToken(token)) {
    return null;
  }
  try {
    const payload = JSON.parse(decodeBase64Url(token.split(".")[1])) as {
      exp?: unknown;
    };
    if (typeof payload.exp === "number" && Number.isFinite(payload.exp) && payload.exp > 0) {
      return payload.exp * 1000;
    }
    return null;
  } catch {
    return null;
  }
}

function storage(): Storage | null {
  return typeof window !== "undefined" ? window.localStorage : null;
}

// setCustomerAccessToken stores a token plus its expiry marker. expiresInSeconds
// comes from the backend (login/register/refresh JSON or the OAuth fragment);
// when absent it falls back to the JWT exp claim. Malformed tokens are REJECTED
// (returns false, nothing stored) so callers never persist attacker input.
export function setCustomerAccessToken(token: string, expiresInSeconds?: number): boolean {
  const store = storage();
  if (!store || !isPlausibleAccessToken(token)) {
    return false;
  }
  let expiresAt: number | null = null;
  if (typeof expiresInSeconds === "number" && Number.isFinite(expiresInSeconds) && expiresInSeconds > 0) {
    expiresAt = Date.now() + expiresInSeconds * 1000;
  } else {
    expiresAt = tokenExpiryMs(token);
  }
  store.setItem(TOKEN_KEY, token);
  if (expiresAt !== null) {
    store.setItem(TOKEN_EXPIRES_AT_KEY, String(expiresAt));
  } else {
    store.removeItem(TOKEN_EXPIRES_AT_KEY);
  }
  return true;
}

// getCustomerAccessToken returns the stored token only while it is well-formed
// and unexpired. An expired or malformed entry is removed eagerly and reported
// as absent, which is what triggers ensureCustomerSession() to refresh via the
// HttpOnly cookie instead of sending a dead Bearer token.
export function getCustomerAccessToken(): string | null {
  const store = storage();
  if (!store) {
    return null;
  }
  const token = store.getItem(TOKEN_KEY);
  if (!token) {
    return null;
  }
  if (!isPlausibleAccessToken(token)) {
    clearCustomerAccessToken();
    return null;
  }
  const stored = Number(store.getItem(TOKEN_EXPIRES_AT_KEY) ?? NaN);
  const expiresAt = Number.isFinite(stored) && stored > 0 ? stored : tokenExpiryMs(token);
  if (expiresAt !== null && Date.now() >= expiresAt - EXPIRY_SKEW_MS) {
    clearCustomerAccessToken();
    return null;
  }
  return token;
}

// clearCustomerAccessToken removes the token AND its expiry marker WITHOUT
// touching the server session. Pair with customerLogout() for a real sign-out.
// Because storage is shared per-origin, clearing here also signs out every
// other open tab on its next read.
export function clearCustomerAccessToken(): void {
  const store = storage();
  if (!store) {
    return;
  }
  store.removeItem(TOKEN_KEY);
  store.removeItem(TOKEN_EXPIRES_AT_KEY);
}

export type OAuthFragmentResult =
  // No usable fragment: nothing to store, nothing to clean.
  | { kind: "none" }
  // Valid delivery: store the token, then clean the fragment from the URL.
  | { kind: "token"; token: string; expiresIn?: number }
  // The fragment CARRIES an access_token key but the value is unusable
  // (empty, oversized, not JWT-shaped). The fragment MUST still be stripped
  // from the URL so attacker input never lingers in history/share, and the
  // failure should surface as a generic sign-in error.
  | { kind: "invalid" };

// consumeOAuthFragment parses the backend's Google callback fragment
// (#access_token=...&expires_in=...). Pure function: the caller (OAuthReceiver)
// performs the actual storage + history cleanup based on the result.
export function consumeOAuthFragment(hash: string): OAuthFragmentResult {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!raw) {
    return { kind: "none" };
  }
  let params: URLSearchParams;
  try {
    params = new URLSearchParams(raw);
  } catch {
    return { kind: "none" };
  }
  if (!params.has("access_token")) {
    return { kind: "none" };
  }
  const token = params.get("access_token") ?? "";
  if (!isPlausibleAccessToken(token)) {
    return { kind: "invalid" };
  }
  const expiresRaw = params.get("expires_in");
  const expiresIn = expiresRaw ? Number(expiresRaw) : undefined;
  return {
    kind: "token",
    token,
    expiresIn:
      typeof expiresIn === "number" && Number.isFinite(expiresIn) && expiresIn > 0
        ? expiresIn
        : undefined,
  };
}

// oauthErrorMessage maps the backend's log-safe auth_error codes (see
// google_auth_handlers.go) to user-friendly messages. Raw internal errors
// never reach the client (SEC-15), so each code covers a class of failure:
// - access_denied: user cancelled or Google denied consent.
// - start_failed: backend could not build the consent redirect (backend error).
// - missing_params / authentication_failed: invalid/expired OAuth state,
//   code exchange failure, or id_token verification failure (callback error).
// - account_exists_link_required / google_identity_taken: account conflicts.
export function oauthErrorMessage(code: string): string {
  switch (code) {
    case "access_denied":
      return "Google sign-in was cancelled. No changes were made to your account.";
    case "start_failed":
      return "Could not start Google sign-in. Please try again.";
    case "missing_params":
      return "Google sign-in was interrupted. Please try again.";
    case "authentication_failed":
      return "Google sign-in could not be completed. Please try again.";
    case "account_exists_link_required":
      return "An account with this email already exists. Please log in with your email and password.";
    case "google_identity_taken":
      return "This Google account is already linked to another user.";
    default:
      return "Google sign-in failed. Please try again.";
  }
}
