// Unit tests for customer access-token storage + OAuth fragment handling.
// Run: npm test (Node built-in runner, no extra dependencies).
import { beforeEach, test } from "node:test";
import assert from "node:assert/strict";

import {
  clearCustomerAccessToken,
  consumeOAuthFragment,
  getCustomerAccessToken,
  isPlausibleAccessToken,
  oauthErrorMessage,
  setCustomerAccessToken,
  tokenExpiryMs,
} from "./authToken.ts";

// --- helpers ---------------------------------------------------------------

function b64url(value: object): string {
  return Buffer.from(JSON.stringify(value)).toString("base64url");
}

// makeJWT builds an UNSIGNED compact JWT. Shape only — the backend verifies
// signatures; the client never does.
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

let store: Storage;

beforeEach(() => {
  store = makeMemoryStorage();
  (globalThis as { window?: unknown }).window = { localStorage: store };
});

// --- token shape validation -------------------------------------------------

test("isPlausibleAccessToken accepts compact JWT shape", () => {
  assert.equal(isPlausibleAccessToken(makeJWT()), true);
});

test("isPlausibleAccessToken rejects garbage, XSS payloads, and oversized input", () => {
  assert.equal(isPlausibleAccessToken(""), false);
  assert.equal(isPlausibleAccessToken("not-a-jwt"), false);
  assert.equal(isPlausibleAccessToken("a.b.c.d"), false); // 4 segments
  assert.equal(isPlausibleAccessToken("a..c"), false); // empty segment
  assert.equal(isPlausibleAccessToken("<script>alert(1)</script>"), false);
  assert.equal(isPlausibleAccessToken("a.b." + "x".repeat(9000)), false); // oversized
  assert.equal(isPlausibleAccessToken("a b.c.d"), false); // illegal charset
});

// --- OAuth fragment parsing (incl. malicious values) ------------------------

test("consumeOAuthFragment extracts a valid token delivery", () => {
  const token = makeJWT(Math.floor(Date.now() / 1000) + 900);
  const result = consumeOAuthFragment(
    `#access_token=${encodeURIComponent(token)}&token_type=Bearer&expires_in=900&provider=google`
  );
  assert.deepEqual(result, { kind: "token", token, expiresIn: 900 });
});

test("consumeOAuthFragment tolerates missing/invalid expires_in", () => {
  const token = makeJWT(Math.floor(Date.now() / 1000) + 900);
  const noExp = consumeOAuthFragment(`#access_token=${token}`);
  assert.deepEqual(noExp, { kind: "token", token, expiresIn: undefined });
  const badExp = consumeOAuthFragment(`#access_token=${token}&expires_in=abc`);
  assert.deepEqual(badExp, { kind: "token", token, expiresIn: undefined });
});
test("consumeOAuthFragment returns none when no access_token key is present", () => {
  assert.deepEqual(consumeOAuthFragment(""), { kind: "none" });
  assert.deepEqual(consumeOAuthFragment("#"), { kind: "none" });
  assert.deepEqual(consumeOAuthFragment("#section"), { kind: "none" });
  assert.deepEqual(consumeOAuthFragment("#foo=bar"), { kind: "none" });
});

test("consumeOAuthFragment flags malicious access_token values as invalid", () => {
  const cases = [
    "#access_token=",
    "#access_token=abc",
    "#access_token=%3Cscript%3Ealert(1)%3C/script%3E",
    `#access_token=${"A".repeat(9000)}`,
    "#access_token=a.b.c.d.e",
  ];
  for (const fragment of cases) {
    const result = consumeOAuthFragment(fragment);
    assert.equal(result.kind, "invalid", `expected invalid for ${fragment.slice(0, 40)}`);
    // An invalid result must NEVER carry a token value.
    assert.equal("token" in result, false);
  }
});

// --- storage: set/get/clear, expiry enforcement -----------------------------

test("setCustomerAccessToken rejects malformed tokens and stores nothing", () => {
  assert.equal(setCustomerAccessToken("garbage"), false);
  assert.equal(getCustomerAccessToken(), null);
});

test("set/get roundtrip with expires_in marker", () => {
  const token = makeJWT(Math.floor(Date.now() / 1000) + 900);
  assert.equal(setCustomerAccessToken(token, 900), true);
  assert.equal(getCustomerAccessToken(), token);
});

test("expired token (past exp claim) is treated as absent and purged", () => {
  const expired = makeJWT(Math.floor(Date.now() / 1000) - 3600);
  assert.equal(setCustomerAccessToken(expired), true);
  assert.equal(getCustomerAccessToken(), null);
  // Purged from storage, not just hidden.
  assert.equal(store.getItem("vero_customer_access_token"), null);
  assert.equal(store.getItem("vero_customer_access_token_expires_at"), null);
});

test("token inside the expiry skew window is treated as expired", () => {
  const almostDead = makeJWT(Math.floor(Date.now() / 1000) + 10); // < 30s skew
  setCustomerAccessToken(almostDead);
  assert.equal(getCustomerAccessToken(), null);
});

test("expires_in marker is honoured over the JWT exp claim", () => {
  const token = makeJWT(Math.floor(Date.now() / 1000) + 3600);
  setCustomerAccessToken(token, 1); // marker says ~now -> inside skew
  assert.equal(getCustomerAccessToken(), null);
});

test("tokenExpiryMs reads the exp claim without verifying the signature", () => {
  const exp = Math.floor(Date.now() / 1000) + 900;
  assert.equal(tokenExpiryMs(makeJWT(exp)), exp * 1000);
  assert.equal(tokenExpiryMs("garbage"), null);
});

// --- logout / multi-tab behaviour -------------------------------------------

test("clearCustomerAccessToken removes token and expiry (logout, all tabs)", () => {
  const token = makeJWT(Math.floor(Date.now() / 1000) + 900);
  setCustomerAccessToken(token, 900);
  clearCustomerAccessToken();
  assert.equal(store.getItem("vero_customer_access_token"), null);
  assert.equal(store.getItem("vero_customer_access_token_expires_at"), null);
  // Multi-tab: storage is shared per-origin, so the next read in ANY tab
  // observes the logout.
  assert.equal(getCustomerAccessToken(), null);
});

test("crafted localStorage value is never returned as a usable token", () => {
  // Simulates an XSS/localStorage-tampering attempt writing a non-JWT value.
  store.setItem("vero_customer_access_token", "attacker-controlled");
  assert.equal(getCustomerAccessToken(), null);
  assert.equal(store.getItem("vero_customer_access_token"), null); // purged
});

// --- error mapping (no echo of attacker input) ------------------------------

test("oauthErrorMessage maps known codes and never echoes unknown input", () => {
  assert.match(oauthErrorMessage("access_denied"), /cancelled/);
  assert.match(oauthErrorMessage("account_exists_link_required"), /already exists/);
  const crafted = "xss-<img src=x onerror=alert(1)>";
  const message = oauthErrorMessage(crafted);
  assert.equal(message.includes(crafted), false);
  assert.equal(message, "Google sign-in failed. Please try again.");
});

