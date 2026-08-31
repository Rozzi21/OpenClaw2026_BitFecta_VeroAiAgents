// Behaviour tests for the customer session helpers in api.ts: refresh on
// expiry, safe logout on refresh failure, logout cleanup, multi-tab dedup,
// and the guarantee that tokens are never logged or placed in URLs.
// Run: npm test (Node built-in runner, fetch/storage stubbed).
import { beforeEach, test } from "node:test";
import assert from "node:assert/strict";

import {
  apiFetch,
  customerLogout,
  ensureCustomerSession,
  getCustomerAccessToken,
  setCustomerAccessToken,
} from "./api.ts";

// --- helpers ---------------------------------------------------------------

function b64url(value: object): string {
  return Buffer.from(JSON.stringify(value)).toString("base64url");
}

function makeJWT(exp?: number): string {
  const payload: Record<string, unknown> = { sub: "user-1", aud: "access" };
  if (exp !== undefined) {
    payload.exp = exp;
  }
  return `${b64url({ alg: "RS256", typ: "JWT" })}.${b64url(payload)}.c2lnYXR1cmU`;
}

function makeMemoryStorage(): Storage {
  const map = new Map<string, string>();
  return {
    getItem: (k: string) => (map.has(k) ? (map.get(k) as string) : null),
    setItem: (k: string, v: string) => void map.set(k, String(v)),
    removeItem: (k: string) => void map.delete(k),
    clear: () => map.clear(),
    key: (i: number) => Array.from(map.keys())[i] ?? null,
    get length() {
      return map.size;
    },
  } as Storage;
}

type FetchCall = { url: string; init: RequestInit };

let store: Storage;
let fetchCalls: FetchCall[];
let fetchHandler: (url: string, init: RequestInit) => Promise<Response>;

function jsonEnvelope(data: unknown, status = 200): Response {
  return new Response(
    JSON.stringify({ success: status < 400, message: status < 400 ? "ok" : "unauthorized", data }),
    { status, headers: { "content-type": "application/json" } }
  );
}

function futureToken(): string {
  return makeJWT(Math.floor(Date.now() / 1000) + 900);
}

beforeEach(() => {
  store = makeMemoryStorage();
  (globalThis as { window?: unknown }).window = { localStorage: store };
  fetchCalls = [];
  fetchHandler = async () => jsonEnvelope({});
  (globalThis as { fetch?: unknown }).fetch = (url: string, init: RequestInit = {}) => {
    fetchCalls.push({ url: String(url), init });
    return fetchHandler(String(url), init);
  };
});

// --- refresh behaviour -------------------------------------------------------

test("valid stored token short-circuits: no refresh request is made", async () => {
  setCustomerAccessToken(futureToken(), 900);
  const state = await ensureCustomerSession();
  assert.equal(state, "active");
  assert.equal(fetchCalls.length, 0);
});

test("expired stored token triggers a refresh via the HttpOnly cookie", async () => {
  const expired = makeJWT(Math.floor(Date.now() / 1000) - 3600);
  setCustomerAccessToken(expired);
  const fresh = futureToken();
  fetchHandler = async () => jsonEnvelope({ access_token: fresh, expires_in: 900 });

  const state = await ensureCustomerSession();
  assert.equal(state, "active");
  assert.equal(fetchCalls.length, 1);
  assert.equal(fetchCalls[0].url, "/api/v1/auth/refresh");
  assert.equal(fetchCalls[0].init.method, "POST");
  assert.equal(getCustomerAccessToken(), fresh);
});

test("refresh 401 (revoked/expired/reused) logs the user out safely", async () => {
  const expired = makeJWT(Math.floor(Date.now() / 1000) - 3600);
  setCustomerAccessToken(expired);
  fetchHandler = async () => jsonEnvelope({}, 401);

  const state = await ensureCustomerSession();
  assert.equal(state, "anonymous");
  // Stale token must be gone — no dead Bearer token left behind.
  assert.equal(store.getItem("vero_customer_access_token"), null);
});

test("concurrent ensureCustomerSession calls share one refresh (multi-tab safe)", async () => {
  const fresh = futureToken();
  fetchHandler = async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
    return jsonEnvelope({ access_token: fresh, expires_in: 900 });
  };

  const [a, b, c] = await Promise.all([
    ensureCustomerSession(),
    ensureCustomerSession(),
    ensureCustomerSession(),
  ]);
  assert.deepEqual([a, b, c], ["active", "active", "active"]);
  assert.equal(fetchCalls.length, 1); // single-use rotation never raced
});

// --- logout ------------------------------------------------------------------

test("customerLogout revokes server-side and clears the local token", async () => {
  setCustomerAccessToken(futureToken(), 900);
  await customerLogout();
  assert.equal(fetchCalls.length, 1);
  assert.equal(fetchCalls[0].url, "/api/v1/auth/logout");
  assert.equal(getCustomerAccessToken(), null);
});

test("customerLogout still clears the local token when the network fails", async () => {
  setCustomerAccessToken(futureToken(), 900);
  fetchHandler = async () => {
    throw new Error("network down");
  };
  await customerLogout();
  assert.equal(getCustomerAccessToken(), null);
});
test("customerLogout still clears the local token when the network fails", async () => {
  setCustomerAccessToken(futureToken(), 900);
  fetchHandler = async () => {
    throw new Error("network down");
  };
  await customerLogout();
  assert.equal(getCustomerAccessToken(), null);
});

// --- token exposure: headers vs URL vs logs ---------------------------------

test("apiFetch attaches the token as a Bearer header and never in the URL", async () => {
  const token = futureToken();
  setCustomerAccessToken(token, 900);
  await apiFetch("/api/v1/auth/me");

  assert.equal(fetchCalls.length, 1);
  const headers = new Headers(fetchCalls[0].init.headers);
  assert.equal(headers.get("Authorization"), `Bearer ${token}`);
  assert.equal(fetchCalls[0].url.includes(token), false);
  assert.equal(fetchCalls[0].url, "/api/v1/auth/me");
});

test("crafted localStorage content is never attached as a Bearer header", async () => {
  store.setItem("vero_customer_access_token", "attacker-controlled");
  await apiFetch("/api/v1/packages");
  const headers = new Headers(fetchCalls[0].init.headers);
  assert.equal(headers.get("Authorization"), null);
});

test("token is never written to console on malformed auth responses", async () => {
  // A proxy/gateway butchers the refresh JSON mid-body. The raw body contains
  // the token; the parser must log metadata only.
  const token = futureToken();
  const malformed = `{"success":true,"message":"ok","data":{"access_token":"${token}"`;
  (globalThis as { fetch?: unknown }).fetch = (url: string, init: RequestInit = {}) => {
    fetchCalls.push({ url: String(url), init });
    return Promise.resolve(
      new Response(malformed, { status: 200, headers: { "content-type": "application/json" } })
    );
  };

  const logged: unknown[] = [];
  const originalError = console.error;
  console.error = (...args: unknown[]) => {
    logged.push(args);
  };
  try {
    await assert.rejects(apiFetch("/api/v1/auth/refresh", { method: "POST" }));
  } finally {
    console.error = originalError;
  }

  assert.equal(logged.length > 0, true); // the failure WAS logged (metadata)
  const serialized = JSON.stringify(logged);
  assert.equal(serialized.includes(token), false, "token leaked into console output");
});

test("token is never written to console on non-JSON error responses", async () => {
  const token = futureToken();
  (globalThis as { fetch?: unknown }).fetch = (url: string, init: RequestInit = {}) => {
    fetchCalls.push({ url: String(url), init });
    return Promise.resolve(
      new Response(`<html>502 bad gateway ${token}</html>`, {
        status: 502,
        headers: { "content-type": "text/html" },
      })
    );
  };

  const logged: unknown[] = [];
  const originalError = console.error;
  console.error = (...args: unknown[]) => {
    logged.push(args);
  };
  try {
    await assert.rejects(apiFetch("/api/v1/packages"));
  } finally {
    console.error = originalError;
  }

  assert.equal(logged.length > 0, true);
  assert.equal(JSON.stringify(logged).includes(token), false);
});

