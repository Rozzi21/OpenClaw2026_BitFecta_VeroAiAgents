// Token storage lives in ./authToken (validated, expiry-aware). Re-exported
// below so existing callers keep working unchanged.
import {
  clearCustomerAccessToken,
  getCustomerAccessToken,
  setCustomerAccessToken,
} from "./authToken.ts";

const SERVER_API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export const API_BASE_URL = SERVER_API_BASE_URL;

function resolveApiBase() {
  if (typeof window !== "undefined") {
    return "";
  }
  return SERVER_API_BASE_URL;
}

export type TripPackage = {
  id: string;
  title: string;
  slug: string;
  destination: string;
  location: string;
  category: string;
  status: string;
  summary: string;
  overview: string;
  duration: string;
  adult_pax?: number;
  child_pax?: number;
  image_url: string;
  media?: Array<{ url: string; type: string; alt_text?: string }>;
  highlights?: string[];
  amenities_included?: string[];
  amenities_excluded?: string[];
  base_price: number;
  estimated_price: number;
  discount_price?: number;
  discount_enabled?: boolean;
  child_price?: number;
  child_discount_price?: number;
  child_discount_enabled?: boolean;
  package_start_date?: string;
  package_end_date?: string;
  itineraries?: Array<{ day: number; title: string; description: string }>;
};

export type BookingOrder = {
  id: string;
  user_id: string;
  trip_id: string;
  booking_status: string;
  payment_status: string;
  total_price: number;
  booking_date: string;
  adult_pax: number;
  child_pax: number;
  contact_name: string;
  contact_email: string;
  contact_phone: string;
  travel_date: string;
  trip?: TripPackage;
};

export type ChatResponse = {
  session_id: string;
  message: string;
  workflow?: Record<string, unknown>[];
  show_recommendations: boolean;
  recommendation_reason: "initial" | "alternative" | "";
  recommended_packages?: TripPackage[];
  // Structured outcome of the ordering step of this turn (backend:
  // services.ChatOrderGate). Absent when the turn did not run create_booking.
  // This is the ONLY thing the UI may branch on for the guest-order rule — the
  // assistant's prose is display text, never a signal.
  order_gate?: ChatOrderGate;
};

// ChatOrderGate is the chat-transport twin of `error.code` on the REST
// endpoints. `code` is stable and backend-owned; `auth_required` is true only
// when the backend refused a guest order because the one-order allowance is
// spent; `order_id` is present only when THIS chat session owns an order, so
// tracking survives the sign-in prompt.
export type ChatOrderGate = {
  code: string;
  auth_required: boolean;
  order_id?: string;
};

export type GuestChatHistoryResponse = {
  messages: Array<{
    role: "user" | "assistant";
    content: string;
  }>;
};

type Envelope<T> = {
  success: boolean;
  message: string;
  data: T;
  error?: unknown;
};

export class APIError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
    public readonly details?: unknown
  ) {
    super(message);
    this.name = "APIError";
  }
}

export {
  clearCustomerAccessToken,
  getCustomerAccessToken,
  setCustomerAccessToken,
} from "./authToken.ts";

type RefreshResponse = { access_token: string; expires_in?: number };

// Refresh outcome. The Google session is a NORMAL Vero session, so the same
// refresh cookie + rotation machinery applies identically to password logins.
export type CustomerSessionState = "active" | "anonymous";

// ensureCustomerSession guarantees a usable access token for an authenticated
// customer. If a token is already stored it is returned as-is; otherwise the
// HttpOnly refresh cookie (set at login/Google callback, path /api/v1/auth) is
// exchanged once for a fresh access token via POST /auth/refresh (atomic
// rotation, same as password login). Returns "active" when a token is
// available, "anonymous" when there is no session (never logged in, or the
// refresh session was revoked/expired/reused — e.g. after logout).
//
// Concurrent callers share ONE in-flight refresh so two tabs/components do not
// race the single-use rotation (the loser would be rejected by reuse
// detection). Only meaningful client-side (needs localStorage + cookies).
let refreshInFlight: Promise<CustomerSessionState> | null = null;

export function ensureCustomerSession(): Promise<CustomerSessionState> {
  if (typeof window === "undefined") return Promise.resolve("anonymous");
  if (getCustomerAccessToken()) return Promise.resolve("active");
  if (refreshInFlight) return refreshInFlight;

  refreshInFlight = (async (): Promise<CustomerSessionState> => {
    try {
      const result = await apiFetch<RefreshResponse>("/api/v1/auth/refresh", { method: "POST" });
      if (result && result.access_token && setCustomerAccessToken(result.access_token, result.expires_in)) {
        return "active";
      }
      return "anonymous";
    } catch (err) {
      // 401 means the refresh session is gone (revoked/expired/reuse-detected):
      // drop any stale stored token so the client logs out safely instead of
      // keeping a dead Bearer token around. Network failures keep the token —
      // the session may still be valid once connectivity returns.
      if (err instanceof APIError && err.status === 401) {
        clearCustomerAccessToken();
      }
      return "anonymous";
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

// customerLogout performs a REAL sign-out: it revokes the server-side refresh
// session (POST /auth/logout reads the HttpOnly cookie and revokes its JTI,
// exactly like a password-login logout) and clears the stored access token.
// Works identically for Google-authenticated sessions (same AuthSession). Safe
// to call when already anonymous. Returns after the local token is cleared.
export async function customerLogout(): Promise<void> {
  if (typeof window === "undefined") return;
  try {
    await apiFetch("/api/v1/auth/logout", { method: "POST" });
  } catch {
    // Network/parse failure — still clear the local token so the client is not
    // stuck appearing logged-in; the server session expires on its own.
  } finally {
    refreshInFlight = null;
    clearCustomerAccessToken();
  }
}

// Abort requests that hang so the UI does not stay in a loading state forever.
const REQUEST_TIMEOUT_MS = 35_000; // slightly above the max AI workflow timeout

async function parseJsonEnvelope<T>(response: Response): Promise<Envelope<T>> {
  const contentType = response.headers.get("content-type") || "";
  // Proxy errors (e.g. 502/504 from Next.js rewrite or nginx) often return HTML.
  // Detect that early and surface a clearer message instead of "invalid JSON".
  const raw = await response.text();
  if (!contentType.includes("application/json") || !raw.trim().startsWith("{")) {
    // SECURITY: never log the response BODY. Auth endpoints (/auth/login,
    // /auth/refresh) return the access token in the body, and console output
    // is captured by error-reporting tooling. Log metadata only.
    // eslint-disable-next-line no-console
    console.error("[api] non-JSON response", {
      status: response.status,
      contentType,
      bodyLength: raw.length,
    });
    if (!response.ok) {
      throw new Error(
        `Server mengalami gangguan (${response.status}). Coba beberapa saat lagi.`
      );
    }
    throw new Error("Server merespons dengan format yang tidak dikenal.");
  }
  try {
    return JSON.parse(raw) as Envelope<T>;
  } catch (err) {
    // SECURITY: same rule as above — the raw body may carry an access token
    // (malformed auth responses), so only metadata is logged.
    // eslint-disable-next-line no-console
    console.error("[api] failed to parse JSON response", {
      status: response.status,
      bodyLength: raw.length,
      error: err instanceof Error ? err.message : String(err),
    });
    throw new Error("Gagal membaca respons dari server. Coba refresh halaman.");
  }
}

export async function apiFetch<T>(path: string, options: RequestInit = {}) {
  const headers = new Headers(options.headers);
	const accessToken = getCustomerAccessToken();
	if (accessToken && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${accessToken}`);
  if (!(options.body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
  }

  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);

  let response: Response;
  try {
    response = await fetch(`${resolveApiBase()}${path}`, {
      ...options,
      headers,
      credentials: options.credentials ?? "include",
      signal: controller.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      throw new Error(
        "Server terlalu lama merespons. Pastikan backend berjalan dan coba lagi."
      );
    }
    throw new Error(
      "Tidak dapat terhubung ke server. Pastikan backend berjalan di http://localhost:8080."
    );
  } finally {
    clearTimeout(timeoutId);
  }

  const payload = await parseJsonEnvelope<T>(response);
  if (!response.ok || !payload.success) {
    const details = payload.error as { code?: string } | undefined;
    throw new APIError(payload.message || "Request failed", response.status, details?.code, payload.error);
  }
  return payload.data;
}

// ChatStreamHandlers describes the callbacks used while consuming the SSE
// streaming chat endpoint (PERF-1). onDelta fires for each text fragment as it
// arrives (append to the in-flight assistant message), onDone fires once with
// the final ChatResult (packages/recommendation flags), onError fires if the
// stream fails mid-flight.
export type ChatStreamHandlers = {
  onDelta: (text: string) => void;
  onDone: (result: ChatResponse) => void;
  onError: (message: string) => void;
};

// parseSSEBlock splits a single raw SSE block (the text between two blank-line
// separators) into its `event` and `data` fields per the SSE wire format.
function parseSSEBlock(raw: string): { event: string; data: string } {
  let event = "message";
  let data = "";
  for (const line of raw.split("\n")) {
    if (line.startsWith("event:")) {
      event = line.slice("event:".length).trim();
    } else if (line.startsWith("data:")) {
      data += line.slice("data:".length).trim();
    }
  }
  return { event, data };
}

// streamChat POSTs a chat request with `stream: true` and consumes the SSE
// response incrementally. Unlike apiFetch, this does NOT apply the 35s abort
// timeout: a streaming response is expected to stay open while tokens flow,
// and the backend caps the whole workflow via AI_TIMEOUT_SECONDS + the request
// context (SEC-26). A client disconnect (AbortController) still propagates and
// cancels the upstream stream.
export async function streamChat(
  path: string,
  payload: Record<string, unknown>,
  handlers: ChatStreamHandlers,
  options: RequestInit = {}
): Promise<void> {
  const headers = new Headers(options.headers);
  headers.set("Content-Type", "application/json");
  // Attach the customer access token when present (same rule as apiFetch):
  // POST /chat accepts an optional Bearer token (OptionalAuth) so a signed-in
  // customer — password or Google — creates chat orders on their ACCOUNT,
  // beyond the one-order guest limit. Callers should run
  // ensureCustomerSession() first so an expired 15-minute token is renewed
  // from the refresh cookie before the stream opens.
  const accessToken = getCustomerAccessToken();
  if (accessToken && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${accessToken}`);

  let response: Response;
  try {
    response = await fetch(`${resolveApiBase()}${path}`, {
      ...options,
      method: "POST",
      headers,
      body: JSON.stringify(payload),
      credentials: options.credentials ?? "include",
      signal: options.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      handlers.onError("Permintaan dibatalkan.");
      return;
    }
    handlers.onError(
      "Tidak dapat terhubung ke server. Pastikan backend berjalan di http://localhost:8080."
    );
    return;
  }

  if (!response.ok || !response.body) {
    // Non-2xx streaming responses are not expected (errors surface as an SSE
    // `error` event), but guard against a proxy returning HTML/JSON anyway.
    let message = `Server mengalami gangguan (${response.status}). Coba beberapa saat lagi.`;
    try {
      const contentType = response.headers.get("content-type") || "";
      if (contentType.includes("application/json")) {
        const envelope = (await response.json()) as Envelope<unknown>;
        if (envelope.message) {
          message = envelope.message;
        }
      }
    } catch {
      // ignore parse error; keep generic message
    }
    handlers.onError(message);
    return;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });

      // SSE events are separated by a blank line. Process every complete block
      // in the buffer; keep the trailing partial block for the next iteration.
      let sep: number;
      while ((sep = buffer.indexOf("\n\n")) >= 0) {
        const rawBlock = buffer.slice(0, sep);
        buffer = buffer.slice(sep + 2);
        if (rawBlock.trim() === "") {
          continue;
        }
        const { event, data } = parseSSEBlock(rawBlock);
        if (!data) {
          continue;
        }
        try {
          if (event === "delta") {
            const parsed = JSON.parse(data) as { content?: string };
            if (parsed.content) {
              handlers.onDelta(parsed.content);
            }
          } else if (event === "done") {
            const parsed = JSON.parse(data) as ChatResponse;
            handlers.onDone(parsed);
          } else if (event === "error") {
            const parsed = JSON.parse(data) as { message?: string };
            handlers.onError(parsed.message ?? "Maaf, Vero belum bisa memproses permintaan ini.");
          }
        } catch {
          // Skip malformed event payloads without aborting the stream.
        }
      }
    }
  } catch {
    handlers.onError("Koneksi terputus saat memuat respons. Coba lagi.");
  }
}

export function assetURL(path?: string) {
  if (!path) {
    return "";
  }
  if (path.startsWith("http")) {
    return path;
  }
  return `${SERVER_API_BASE_URL}${path}`;
}
