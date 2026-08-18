"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { apiFetch, BookingOrder, getCustomerAccessToken } from "@/lib/api";

export default function GuestOrderPage({ params }: { params: { id: string } }) {
  const [order, setOrder] = useState<BookingOrder | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
	const path = getCustomerAccessToken() ? `/api/v1/bookings/${params.id}` : `/api/v1/orders/${params.id}`;
    apiFetch<BookingOrder>(path)
      .then(setOrder)
      .catch(() => setError("Order tidak ditemukan atau bukan milik sesi guest ini."));
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