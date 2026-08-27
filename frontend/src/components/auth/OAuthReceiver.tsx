"use client";

import { useEffect } from "react";
import { setCustomerAccessToken } from "@/lib/api";

// OAuthReceiver consumes the backend's Google callback redirect. The access
// token arrives in the URL fragment (#access_token=...) — which is never sent
// to any server — so this runs client-side, stores the token via the existing
// customer-token helper, then strips the fragment from the address bar.
//
// It also surfaces a backend auth_error (?auth_error=...) so the hosting page
// can show a message.
export function OAuthReceiver({ onError }: { onError?: (message: string) => void }) {
  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const hash = window.location.hash.startsWith("#")
      ? window.location.hash.slice(1)
      : window.location.hash;
    if (hash) {
      const params = new URLSearchParams(hash);
      const token = params.get("access_token");
      if (token) {
        setCustomerAccessToken(token);
        // Clean the fragment so the token does not linger in history/share.
        const clean = window.location.pathname + window.location.search;
        window.history.replaceState(null, "", clean);
        // Land the user on the page they started from (this page) — token is
        // set, so a plain reload of the current path reflects the session.
        window.location.replace(clean);
        return;
      }
    }
    const query = new URLSearchParams(window.location.search);
    const authError = query.get("auth_error");
    if (authError && onError) {
      onError(oauthErrorMessage(authError));
    }
  }, [onError]);

  return null;
}

// Maps the backend's log-safe auth_error codes (see google_auth_handlers.go)
// to user-friendly messages. Raw internal errors never reach the client
// (SEC-15), so each code covers a class of failure:
// - access_denied: user cancelled or Google denied consent.
// - start_failed: backend could not build the consent redirect (backend error).
// - missing_params / authentication_failed: invalid/expired OAuth state,
//   code exchange failure, or id_token verification failure (callback error).
// - account_exists_link_required / google_identity_taken: account conflicts.
function oauthErrorMessage(code: string): string {
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
