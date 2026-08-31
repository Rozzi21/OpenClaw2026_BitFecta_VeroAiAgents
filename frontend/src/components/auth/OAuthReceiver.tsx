"use client";

import { useEffect } from "react";
import { consumeOAuthFragment, oauthErrorMessage, setCustomerAccessToken } from "@/lib/authToken";

// OAuthReceiver consumes the backend's Google callback redirect. The access
// token arrives in the URL fragment (#access_token=...) — which is never sent
// to any server — so this runs client-side, validates and stores the token via
// the shared token helpers, then strips the fragment from the address bar.
//
// Hardening notes:
// - The fragment value is validated (JWT shape + size cap) before storage, so
//   a crafted/malicious fragment is never persisted or later sent as Bearer.
// - ANY fragment carrying an access_token key is removed from the URL, even
//   when invalid, so attacker input never lingers in history/share.
// - It also surfaces a backend auth_error (?auth_error=...) so the hosting page
//   can show a message, then strips that code from the URL too.
export function OAuthReceiver({ onError }: { onError?: (message: string) => void }) {
  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const result = consumeOAuthFragment(window.location.hash);
    if (result.kind !== "none") {
      // Clean the fragment FIRST so the token (or attacker input) does not
      // linger in history/share, whatever happens next.
      const clean = window.location.pathname + window.location.search;
      window.history.replaceState(null, "", clean);
      if (result.kind === "token" && setCustomerAccessToken(result.token, result.expiresIn)) {
        // Land the user on the page they started from (this page) — token is
        // set, so a plain reload of the current path reflects the session.
        window.location.replace(clean);
        return;
      }
      // kind === "invalid" (or storage rejected the token): fragment already
      // stripped; surface a generic failure. The raw value is never shown or
      // logged.
      if (result.kind === "invalid" && onError) {
        onError(oauthErrorMessage("authentication_failed"));
      }
    }
    const query = new URLSearchParams(window.location.search);
    const authError = query.get("auth_error");
    if (authError) {
      if (onError) {
        onError(oauthErrorMessage(authError));
      }
      // Strip the error code from the address bar so it does not persist in
      // history or get re-shown on the next mount.
      query.delete("auth_error");
      const remaining = query.toString();
      window.history.replaceState(
        null,
        "",
        window.location.pathname + (remaining ? `?${remaining}` : "")
      );
    }
  }, [onError]);

  return null;
}

