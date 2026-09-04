// Header allowlist of the SSE chat proxy (src/app/api/v1/chat/route.ts).
//
// GO-P1-1 regression guard: the proxy used to drop Authorization, so a signed-in
// customer chatting through it was seen as a guest by the backend and hit
// GUEST_ORDER_LIMIT_REACHED on their SECOND order even though their account is
// entitled to more. The header must survive the hop.
// Run: npm test
import { test } from "node:test";
import assert from "node:assert/strict";

import { forwardedChatHeaders } from "./chatProxy.ts";

test("Authorization is forwarded so a signed-in customer is not treated as a guest", () => {
  const headers = forwardedChatHeaders(
    new Headers({ authorization: "Bearer token-abc", cookie: "vero_chat_session=1" })
  );
  assert.equal(headers.Authorization, "Bearer token-abc");
  assert.equal(headers.Cookie, "vero_chat_session=1");
});

test("cookies and request id still pass through, content type is forced to JSON", () => {
  const headers = forwardedChatHeaders(
    new Headers({ "x-request-id": "req-1", "content-type": "text/plain" })
  );
  assert.equal(headers["X-Request-ID"], "req-1");
  assert.equal(headers["Content-Type"], "application/json");
});

test("absent headers are omitted rather than sent empty", () => {
  const headers = forwardedChatHeaders(new Headers());
  assert.deepEqual(Object.keys(headers), ["Content-Type"]);
});

test("headers outside the allowlist are dropped", () => {
  const headers = forwardedChatHeaders(
    new Headers({
      authorization: "Bearer token-abc",
      host: "evil.example.com",
      origin: "https://evil.example.com",
      "x-forwarded-for": "10.0.0.1",
      "content-length": "999999",
    })
  );
  assert.deepEqual(Object.keys(headers).sort(), ["Authorization", "Content-Type"]);
});
