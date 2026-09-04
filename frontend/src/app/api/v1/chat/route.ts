import { NextRequest } from "next/server";
import { forwardedChatHeaders } from "@/lib/chatProxy";

// SSE streaming proxy for POST /api/v1/chat.
//
// PERF-1 follow-up (5 Agu 2026): Next.js `rewrites()` proxy buffers the
// entire SSE response before forwarding it to the browser, so token-by-token
// deltas arrive all at once and React batches the state updates — the user
// sees the full text appear instantaneously instead of streaming like
// ChatGPT. This route handler bypasses the rewrite proxy by forwarding the
// request server-side (no CORS, no cookie SameSite issues) and piping the
// backend's ReadableStream directly back as the response body. Next.js App
// Router route handlers support streaming responses natively, so each SSE
// chunk is flushed to the client as soon as it arrives from the backend.
//
// Only POST is exported; GET /api/v1/chat/history and other /api/* paths
// still use the Next.js rewrite proxy (which works fine for non-streaming
// JSON).

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const BACKEND_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export async function POST(request: NextRequest) {
  const body = await request.text();

  // Forward only the allowlisted headers (see lib/chatProxy.ts). Server-side
  // fetch is not subject to CORS, so Cookie can be forwarded directly; the
  // Authorization header must be forwarded too, otherwise a signed-in customer
  // is treated as a guest by the backend and runs into the one-order guest
  // limit from the chat (GO-P1-1).
  const headers = forwardedChatHeaders(request.headers);

  let backendResponse: Response;
  try {
    backendResponse = await fetch(`${BACKEND_URL}/api/v1/chat`, {
      method: "POST",
      headers,
      body,
    });
  } catch {
    return new Response(
      JSON.stringify({
        success: false,
        message:
          "Tidak dapat terhubung ke server. Pastikan backend berjalan di http://localhost:8080.",
        data: null,
      }),
      {
        status: 502,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

  // Build response headers. We must preserve the SSE content type and
  // disable buffering at every layer.
  const responseHeaders = new Headers();
  const contentType = backendResponse.headers.get("Content-Type");
  responseHeaders.set(
    "Content-Type",
    contentType || "text/event-stream"
  );
  responseHeaders.set("Cache-Control", "no-cache");
  responseHeaders.set("Connection", "keep-alive");
  // Hint reverse proxies (Nginx/Caddy) not to buffer the stream.
  responseHeaders.set("X-Accel-Buffering", "no");

  // Forward Set-Cookie headers from the backend so the guest session cookie
  // is set on the client's origin (same-origin, no SameSite issues).
  const setCookies = backendResponse.headers.getSetCookie();
  for (const cookie of setCookies) {
    responseHeaders.append("Set-Cookie", cookie);
  }

  if (!backendResponse.body) {
    return new Response(
      JSON.stringify({
        success: false,
        message: "Backend returned an empty response.",
        data: null,
      }),
      {
        status: 502,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

  // Pipe the backend's ReadableStream directly as the response body.
  // Next.js App Router streams this to the client without buffering.
  return new Response(backendResponse.body, {
    status: backendResponse.status,
    headers: responseHeaders,
  });
}