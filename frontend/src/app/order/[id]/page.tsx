"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { apiFetch, BookingOrder, ensureCustomerSession } from "@/lib/api";

export default function GuestOrderPage({ params }: { params: { id: string } }) {
  const [order, setOrder] = useState<BookingOrder | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    (async () => {
      // Renew the access token from the refresh cookie first: after the guest
      // order is CLAIMED to an account (login/Google), bookings.guest_session_id
      // becomes NULL, so the guest endpoint can no longer see it — the
      // authenticated endpoint is required.
      const authenticated = (await ensureCustomerSession()) === "active";
      const path = authenticated ? `/api/v1/bookings/${params.id}` : `/api/v1/orders/${params.id}`;
      try {
        const result = await apiFetch<BookingOrder>(path);
        if (!cancelled) setOrder(result);
      } catch {
        // Authenticated but the account cannot see the order: the claim hook at
        // login/Google callback is best-effort and may have been skipped (the
        // guest cookie is not sent on the cross-site Google callback when
        // SameSite is too strict) — the order is then stranded on the guest
        // identity. Re-run the claim once; it is idempotent and takes no order
        // id, so it only ever moves THIS browser's own guest order (401/404/409
        // otherwise) and never the order being viewed.
        if (authenticated) {
          try {
            await apiFetch<{ order_id: string; transferred: boolean }>(
              "/api/v1/orders/claim",
              { method: "POST" }
            );
            const claimed = await apiFetch<BookingOrder>(path);
            if (!cancelled) setOrder(claimed);
            return;
          } catch {
            // Nothing to claim, or the order belongs to someone else: fall
            // through to the generic message below.
          }
        }
        if (!cancelled) setError("Order tidak ditemukan atau bukan milik sesi guest ini.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [params.id]);

  return (
    <main className="min-h-screen bg-slate-50 px-6 py-16">
      <div className="mx-auto max-w-2xl rounded-3xl bg-white p-8 shadow-sm">
        <Link href="/" className="text-sm font-bold text-[#df3333]">Kembali ke chat</Link>
        <h1 className="mt-6 text-3xl font-black text-slate-900">Guest Order Tracking</h1>
        {error ? <p className="mt-6 rounded-xl bg-rose-50 p-4 text-rose-700">{error}</p> : null}
        {!order && !error ? <p className="mt-6 text-slate-500">Memuat order...</p> : null}
        {order ? (
          <dl className="mt-8 grid gap-4 rounded-2xl bg-slate-50 p-6 text-sm">
            <div><dt className="text-slate-500">Order ID</dt><dd className="font-bold">{order.id}</dd></div>
            <div><dt className="text-slate-500">Booking Status</dt><dd className="font-bold capitalize">{order.booking_status}</dd></div>
            <div><dt className="text-slate-500">Payment Status</dt><dd className="font-bold capitalize">{order.payment_status}</dd></div>
          </dl>
        ) : null}
      </div>
    </main>
  );
}