// Header allowlist for the SSE chat proxy (src/app/api/v1/chat/route.ts).
//
// The proxy is a server-side hop: the browser talks to the Next.js origin and
// this module decides what is forwarded to the Go backend. Everything else
// (Host, Origin, Referer, Content-Length, Accept-Encoding, X-Forwarded-*) is
// dropped on purpose — a rewritten Host breaks TLS/virtual hosting and a stale
// Content-Length corrupts the forwarded body.
//
// GO-P1-1 (fixed): `Authorization` used to be missing from this list. streamChat
// attaches the customer access token, but the proxy stripped it, so the backend
// saw every chat request as anonymous: a signed-in customer went down the guest
// booking path and hit GUEST_ORDER_LIMIT_REACHED even though their account is
// allowed further orders. Forwarding it is what makes "sign in, then order
// again" work from the chat, and it is the only place that can: the backend's
// OptionalAuth middleware reads the Bearer token from this header.
//
// The token itself is not inspected or logged here — it is copied verbatim and
// verified by the backend (a malformed/expired token simply degrades the request
// to guest, which is the pre-existing behaviour).
const FORWARDED_REQUEST_HEADERS = [
  "authorization",
  "cookie",
  "x-request-id",
] as const;

// forwardedChatHeaders builds the outgoing header map for the proxied request.
// Content-Type is always application/json (the backend chat endpoint only
// accepts JSON); the allowlisted headers are copied when present.
export function forwardedChatHeaders(incoming: Headers): Record<string, string> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  for (const name of FORWARDED_REQUEST_HEADERS) {
    const value = incoming.get(name);
    if (value) {
      // Canonical casing for readability; HTTP header names are
      // case-insensitive so the backend accepts either form.
      headers[canonicalHeaderName(name)] = value;
    }
  }
  return headers;
}

function canonicalHeaderName(lower: string): string {
  return lower
    .split("-")
    .map((part) => (part === "id" ? "ID" : part.charAt(0).toUpperCase() + part.slice(1)))
    .join("-");
}
