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
      onError("Google sign-in failed. Please try again.");
    }
  }, [onError]);

  return null;
}
