"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  ArrowRight,
  CalendarDays,
  Check,
  CheckCircle2,
  Copy,
  CreditCard,
  FilterX,
  LoaderCircle,
  Mail,
  MapPin,
  MessageCircle,
  PackageCheck,
  RefreshCw,
  Search,
  SlidersHorizontal,
  UserRound,
  UsersRound,
  X,
  XCircle,
} from "lucide-react";
import { BookingOrder, BookingStatus } from "@/lib/api";
import { formatIDR } from "@/lib/format";
import { cn } from "@/lib/utils";
import { fetchOrders, updateOrderStatus } from "@/lib/order";
import { ToastNotification, ToastState } from "@/components/toast-notification";
import { ConfirmModal } from "@/components/confirm-modal";

type StatusFilter = "all" | BookingStatus;

const filters: Array<{ value: StatusFilter; label: string }> = [
  { value: "all", label: "All Orders" },
  { value: "pending", label: "New" },
  { value: "processing", label: "Processing" },
  { value: "confirmed", label: "Confirmed" },
  { value: "completed", label: "Completed" },
  { value: "cancelled", label: "Cancelled" },
];

const statusLabels: Record<BookingStatus, string> = {
  pending: "NEW",
  processing: "PROCESSING",
  confirmed: "CONFIRMED",
  completed: "COMPLETED",
  cancelled: "CANCELLED",
};

const statusTone: Record<BookingStatus, string> = {
  pending: "bg-[#c1121f] text-white",
  processing: "bg-[#eef0ff] text-[#111827]",
  confirmed: "bg-[#dcfce7] text-[#166534]",
  completed: "bg-[#f0e7ff] text-[#5b21b6]",
  cancelled: "bg-[#fee2e2] text-[#991b1b]",
};

const statusRank: Record<BookingStatus, number> = {
  pending: 0,
  processing: 1,
  confirmed: 2,
  completed: 3,
  cancelled: 4,
};

const paymentTone = {
  pending: "bg-amber-50 text-amber-700 ring-amber-100",
  paid: "bg-emerald-50 text-emerald-700 ring-emerald-100",
  cancelled: "bg-rose-50 text-rose-700 ring-rose-100",
  neutral: "bg-slate-50 text-slate-600 ring-slate-200",
};

const transitionLabels: Partial<Record<BookingStatus, string>> = {
  pending: "Process Order",
  processing: "Update Status",
  confirmed: "Complete Order",
};

function normalizeStatus(status: string): BookingStatus {
  switch (status) {
    case "processing":
    case "confirmed":
    case "completed":
    case "cancelled":
      return status;
    default:
      return "pending";
  }
}

function nextStatus(status: BookingStatus): BookingStatus | null {
  if (status === "pending") return "processing";
  if (status === "processing") return "confirmed";
  if (status === "confirmed") return "completed";
  return null;
}

function customerName(order: BookingOrder) {
  return order.contact_name || order.user?.name || "Guest Traveler";
}

function customerEmail(order: BookingOrder) {
  const raw = order.contact_email || order.user?.email || "";
  return sanitizeEmail(raw);
}

function packageName(order: BookingOrder) {
  return order.trip?.title || order.trip_id;
}

function destination(order: BookingOrder) {
  return order.trip?.destination || order.trip?.location || "-";
}

function formatDate(value?: string) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("en", {
    day: "2-digit",
    month: "short",
    year: "numeric",
  }).format(new Date(value));
}

function formatDateTime(value?: string) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("id-ID", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function formatRelative(value?: string) {
  if (!value) return "-";
  const diff = Date.now() - new Date(value).getTime();
  const minutes = Math.max(1, Math.floor(diff / 60000));
  if (minutes < 60) return `${minutes} minutes ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} hours ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

function travelDate(order: BookingOrder) {
  if (order.travel_date) return formatDate(order.travel_date);
  const start = order.trip?.package_start_date;
  const end = order.trip?.package_end_date;
  if (start && end) return `${formatDate(start)} - ${formatDate(end)}`;
  if (start) return formatDate(start);
  return "Flexible schedule";
}

function travelers(order: BookingOrder) {
  const adults = order.adult_pax || 0;
  const children = order.child_pax || 0;
  const adultLabel = `${adults} ${adults === 1 ? "Adult" : "Adults"}`;
  const childLabel = `${children} ${children === 1 ? "Child" : "Children"}`;
  return children > 0 ? `${adultLabel}, ${childLabel}` : adultLabel;
}

function sanitizePhone(phone: string) {
  const normalized = phone.replace(/[^0-9]/g, "").replace(/^0/, "62");
  return /^[0-9]{7,15}$/.test(normalized) ? normalized : "";
}

function whatsappUrl(phone: string) {
  const normalized = sanitizePhone(phone);
  return normalized ? `https://wa.me/${normalized}` : "";
}

function sanitizeEmail(email: string) {
  const trimmed = email.trim();
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed) ? trimmed : "";
}

function paymentStatus(order: BookingOrder) {
  const value = order.payment_status?.trim().toLowerCase();
  if (!value || value === "pending_admin_processing") {
    return null;
  }
  if (["paid", "settlement", "success"].includes(value)) {
    return { label: "Lunas", tone: paymentTone.paid };
  }
  if (["cancelled", "failed", "expired"].includes(value)) {
    return { label: "Tidak aktif", tone: paymentTone.cancelled };
  }
  return { label: value.replaceAll("_", " "), tone: paymentTone.neutral };
}

function StatsCard({
  title,
  value,
  note,
  icon,
  prominent,
}: {
  title: string;
  value: number;
  note: string;
  icon: React.ReactNode;
  prominent?: boolean;
}) {
  return (
    <div
      className={cn(
        "min-h-[102px] rounded-xl border p-4 shadow-[0_12px_26px_-20px_rgba(17,24,39,0.7)]",
        prominent
          ? "border-[#c1121f] bg-[#c1121f] text-white"
          : "border-[#e4e7f2] bg-white text-[#111827]"
      )}
    >
      <div className="flex items-center justify-between gap-3">
        <p className="text-[10px] font-bold uppercase tracking-[0.13em]">{title}</p>
        <span className={cn("flex h-8 w-8 items-center justify-center rounded-xl", prominent ? "bg-white/15" : "bg-[#eef0ff] text-[#273148]")}>{icon}</span>
      </div>
      <div className="mt-2 flex items-center justify-between">
        <p className="text-3xl font-extrabold tracking-[-0.04em]">
          {value.toLocaleString("id-ID")}
        </p>
        {prominent && <span className="rounded border border-white/30 px-1.5 py-1 text-[9px] font-extrabold">ACTION</span>}
      </div>
      <p
        className={cn(
          "mt-3 inline-flex items-center gap-1 text-[10px] font-bold",
          prominent ? "rounded-full bg-white/15 px-2 py-1" : "text-[#7b7f8c]"
        )}
      >
        {note}
      </p>
    </div>
  );
}

function OrderCard({
  order,
  busy,
  onView,
  onAdvance,
  onCancel,
}: {
  order: BookingOrder;
  busy: boolean;
  onView: (order: BookingOrder) => void;
  onAdvance: (order: BookingOrder) => void;
  onCancel: (order: BookingOrder) => void;
}) {
  const status = normalizeStatus(order.booking_status);
  const next = nextStatus(status);
  const payment = paymentStatus(order);

  return (
    <article
      className={cn(
        "rounded-2xl border bg-white p-4 shadow-[0_12px_26px_-20px_rgba(17,24,39,0.7)]",
        status === "pending" ? "border-l-2 border-l-[#c1121f] border-[#e4e7f2]" : "border-[#e4e7f2]"
      )}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="text-[10px] font-bold uppercase text-[#606473]">#{order.id.slice(0, 8)}</div>
        <span className={cn("rounded px-2 py-0.5 text-[9px] font-extrabold", statusTone[status])}>
          {statusLabels[status]}
        </span>
      </div>

      <div className="mt-2 flex items-start justify-between gap-4">
        <h2 className="text-lg font-extrabold leading-tight tracking-[-0.03em] text-[#111827]">
          {packageName(order)}
        </h2>
        <div className="text-right">
          <p className="text-[10px] font-bold text-[#606473]">Total harga</p>
          <p className="font-extrabold text-[#c1121f]">{formatIDR(order.total_price)}</p>
        </div>
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-extrabold uppercase tracking-wide", statusTone[status])}>
          <span className="h-1.5 w-1.5 rounded-full bg-current" />
          {statusLabels[status]}
        </span>
        {payment && (
          <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-bold capitalize ring-1 ring-inset", payment.tone)}>
            <CreditCard size={12} />
            {payment.label}
          </span>
        )}
      </div>

      <div className="mt-5 grid grid-cols-1 gap-4 text-xs text-[#111827] sm:grid-cols-2">
        <Info icon={<UserRound size={15} />} label="Pelanggan" value={customerName(order)} subValue={order.contact_phone ? "WhatsApp tersedia" : undefined} />
        <Info icon={<CalendarDays size={15} />} label="Tanggal perjalanan" value={travelDate(order)} />
        <Info icon={<MapPin size={15} />} label="Destinasi" value={destination(order)} />
        <Info icon={<UsersRound size={15} />} label="Wisatawan" value={travelers(order)} />
      </div>

      <div className="mt-6 flex flex-wrap items-center justify-between gap-3 border-t border-[#e4e7f2] pt-4">
        <p className="text-[10px] font-bold text-[#7b7f8c]">Masuk {formatRelative(order.created_at || order.booking_date)}</p>
        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={() => onView(order)} className="h-9 rounded-xl px-4 text-xs font-bold text-[#111827] hover:bg-[#eef0ff]">
            Lihat detail
          </button>
          {status !== "completed" && status !== "cancelled" && (
            <button
              type="button"
              disabled={busy}
              onClick={() => onCancel(order)}
              className="h-9 rounded-xl border border-[#fee2e2] px-4 text-xs font-bold text-[#991b1b] transition hover:bg-[#fee2e2] disabled:cursor-not-allowed disabled:opacity-60"
            >
              Batalkan
            </button>
          )}
          {next && (
            <button
              type="button"
              disabled={busy}
              onClick={() => onAdvance(order)}
              className="inline-flex h-9 items-center gap-2 rounded-xl bg-[#111827] px-4 text-xs font-extrabold text-white shadow-[0_12px_26px_-20px_rgba(17,24,39,0.7)] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {busy ? "Memproses..." : transitionLabels[status]}
              {!busy && <ArrowRight size={14} />}
            </button>
          )}
        </div>
      </div>
    </article>
  );
}

function Info({ icon, label, value, subValue }: { icon: React.ReactNode; label: string; value: string; subValue?: string }) {
  return (
    <div className="flex gap-3">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[#eef0ff] text-[#111827]">{icon}</span>
      <div>
        <p className="text-[10px] font-bold text-[#7b7f8c]">{label}</p>
        <p className="mt-0.5 font-semibold text-[#111827]">{value}</p>
        {subValue && <p className="mt-0.5 text-[10px] font-bold text-emerald-600">{subValue}</p>}
      </div>
    </div>
  );
}

function DetailDrawer({ order, onClose, onAdvance, onCancel, busy }: { order: BookingOrder | null; onClose: () => void; onAdvance: (order: BookingOrder) => void; onCancel: (order: BookingOrder) => void; busy: boolean }) {
  if (!order) return null;
  const status = normalizeStatus(order.booking_status);
  const next = nextStatus(status);
  const wa = order.contact_phone ? whatsappUrl(order.contact_phone) : "";
  const email = customerEmail(order);
  const payment = paymentStatus(order);

  return (
    <div className="fixed inset-0 z-[100] bg-black/30" onClick={onClose}>
      <aside
        className="ml-auto flex h-full w-full max-w-xl flex-col overflow-y-auto bg-white shadow-[0_12px_26px_-20px_rgba(17,24,39,0.7)]"
        role="dialog"
        aria-modal="true"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="sticky top-0 z-10 flex items-start justify-between gap-4 border-b border-[#e4e7f2] bg-white/95 p-6 backdrop-blur">
          <div>
            <p className="text-xs font-extrabold uppercase tracking-[0.2em] text-[#c1121f]">Order Detail</p>
            <h2 className="mt-2 text-2xl font-extrabold text-[#111827]">#{order.id.slice(0, 8)}</h2>
          </div>
          <button type="button" onClick={onClose} className="rounded-full p-2 text-[#6b7280] hover:bg-[#eef0ff]" aria-label="Close order detail">
            <X size={20} />
          </button>
        </div>

        <div className="space-y-7 p-6">
          <Section title="ORDER INFORMATION">
            <Detail label="Order ID" value={order.id} />
            <Detail label="Created date" value={formatDateTime(order.created_at || order.booking_date)} />
            <Detail label="Current status" value={statusLabels[status]} />
            {payment && <Detail label="Payment status" value={payment.label} />}
          </Section>

          <Section title="CUSTOMER INFORMATION">
            <Detail label="Customer name" value={customerName(order)} />
            <CopyableField label="WhatsApp / phone" value={order.contact_phone || ""} placeholder="Not provided" />
            <CopyableField label="Email" value={email} placeholder="Not provided" />
            <div className="flex flex-wrap gap-2 pt-2">
              {wa && (
                <a href={wa} target="_blank" rel="noreferrer" className="inline-flex h-10 items-center gap-2 rounded-xl bg-emerald-600 px-4 text-xs font-extrabold text-white hover:bg-emerald-700 transition">
                  <MessageCircle size={14} /> Chat WhatsApp
                </a>
              )}
              {email && (
                <a href={`mailto:${email}`} className="inline-flex h-10 items-center gap-2 rounded-xl bg-[#111827] px-4 text-xs font-extrabold text-white hover:bg-[#1f2937] transition">
                  <Mail size={14} /> Send Email
                </a>
              )}
            </div>
          </Section>

          <Section title="TRIP INFORMATION">
            <Detail label="Package name" value={packageName(order)} />
            <Detail label="Destination" value={destination(order)} />
            <Detail label="Duration" value={order.trip?.duration || "Not provided"} />
            <Detail label="Travel date" value={travelDate(order)} />
          </Section>

          <Section title="TRAVELERS">
            <Detail label="Adults" value={String(order.adult_pax || 0)} />
            <Detail label="Children" value={String(order.child_pax || 0)} />
            <Detail label="Total travelers" value={String((order.adult_pax || 0) + (order.child_pax || 0))} />
          </Section>

          <Section title="PRICE SUMMARY">
            <Detail label="Package/base price" value={formatIDR(order.trip?.base_price || order.trip?.estimated_price || 0)} />
            <Detail label="Adult price" value={formatIDR(order.trip?.discount_price || order.trip?.base_price || order.trip?.estimated_price || 0)} />
            <Detail label="Child price" value={order.trip?.child_price ? formatIDR(order.trip.child_price) : "Not provided"} />
            <Detail label="Estimated/total price" value={formatIDR(order.total_price)} />
          </Section>

          <Section title="PACKAGE INFORMATION">
            {order.trip?.highlights?.length ? (
              <ul className="list-disc space-y-1 pl-5 text-sm font-medium text-[#7b7f8c]">
                {order.trip.highlights.map((item) => <li key={item}>{item}</li>)}
              </ul>
            ) : (
              <Detail label="Highlights" value="Not provided" />
            )}
          </Section>

          <Section title="ADMIN INFORMATION">
            <Detail label="Internal order status" value={order.booking_status} />
            <Detail label="Admin notes" value="Not supported by backend" />
          </Section>

          {status !== "completed" && status !== "cancelled" && (
            <div className="flex gap-3">
              <button
                type="button"
                disabled={busy}
                onClick={() => onCancel(order)}
                className="inline-flex h-12 flex-1 items-center justify-center gap-2 rounded-xl border border-[#fee2e2] text-sm font-bold text-[#991b1b] transition hover:bg-[#fee2e2] disabled:cursor-not-allowed disabled:opacity-60"
              >
                Cancel Order
              </button>
              {next && (
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => onAdvance(order)}
                  className="inline-flex h-12 flex-1 items-center justify-center gap-2 rounded-xl bg-[#111827] text-sm font-extrabold text-white disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {busy ? "Processing..." : transitionLabels[status]}
                  {!busy && <ArrowRight size={16} />}
                </button>
              )}
            </div>
          )}
        </div>
      </aside>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-[#e4e7f2] p-5">
      <h3 className="text-xs font-extrabold tracking-[0.18em] text-[#c1121f]">{title}</h3>
      <div className="mt-4 space-y-3">{children}</div>
    </section>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-4 text-sm">
      <span className="font-bold text-[#7b7f8c]">{label}</span>
      <span className="max-w-[60%] text-right font-semibold text-[#111827]">{value}</span>
    </div>
  );
}

function CopyableField({ label, value, placeholder }: { label: string; value: string; placeholder?: string }) {
  const [copied, setCopied] = useState(false);
  const display = value || placeholder || "Not provided";
  const canCopy = Boolean(value);

  const handleCopy = async () => {
    if (!canCopy) return;
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch { /* noop */ }
  };

  return (
    <div className="flex items-center justify-between gap-4 text-sm">
      <span className="font-bold text-[#7b7f8c]">{label}</span>
      <div className="flex items-center gap-2">
        <span className={cn("font-semibold", canCopy ? "text-[#111827]" : "text-[#7b7f8c]")}>{display}</span>
        {canCopy && (
          <button
            type="button"
            onClick={handleCopy}
            className="flex h-7 w-7 items-center justify-center rounded-lg text-[#7b7f8c] transition hover:bg-[#eef0ff] hover:text-[#111827]"
            title="Copy"
          >
            {copied ? <Check size={14} className="text-emerald-600" /> : <Copy size={14} />}
          </button>
        )}
      </div>
    </div>
  );
}

function Skeleton() {
  return <div className="h-60 animate-pulse rounded-2xl bg-white/80 shadow-[0_12px_26px_-20px_rgba(17,24,39,0.7)]" />;
}

export function OrdersPanel() {
  const [orders, setOrders] = useState<BookingOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<StatusFilter>("all");
  const [selected, setSelected] = useState<BookingOrder | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [toast, setToast] = useState<ToastState | null>(null);
  const [cancelCandidate, setCancelCandidate] = useState<BookingOrder | null>(null);

  const loadOrders = useCallback(async () => {
    setError(null);
    try {
      const data = await fetchOrders();
      setOrders(data);
      setSelected((current) => current ? data.find((item) => item.id === current.id) ?? current : null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Gagal memuat order dari backoffice.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    void loadOrders();
  }, [loadOrders]);

  const stats = useMemo(() => {
    const today = new Date().toDateString();
    const byStatus = (status: BookingStatus) => orders.filter((order) => normalizeStatus(order.booking_status) === status).length;
    const todayNew = orders.filter((order) => normalizeStatus(order.booking_status) === "pending" && new Date(order.created_at || order.booking_date).toDateString() === today).length;
    return { pending: byStatus("pending"), processing: byStatus("processing"), confirmed: byStatus("confirmed"), completed: byStatus("completed"), todayNew };
  }, [orders]);

  const filteredOrders = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return orders.filter((order) => {
      const status = normalizeStatus(order.booking_status);
      const matchesFilter = filter === "all" || status === filter;
      const haystack = `${order.id} ${customerName(order)} ${packageName(order)}`.toLowerCase();
      return matchesFilter && (!needle || haystack.includes(needle));
    }).sort((left, right) => {
      const statusDiff = statusRank[normalizeStatus(left.booking_status)] - statusRank[normalizeStatus(right.booking_status)];
      if (statusDiff !== 0) return statusDiff;
      return new Date(right.created_at || right.booking_date).getTime() - new Date(left.created_at || left.booking_date).getTime();
    });
  }, [filter, orders, query]);

  const handleAdvance = async (order: BookingOrder) => {
    const status = normalizeStatus(order.booking_status);
    const target = nextStatus(status);
    if (!target) return;
    setBusyId(order.id);
    try {
      const updated = await updateOrderStatus(order.id, target);
      setOrders((current) => current.map((item) => item.id === order.id ? updated : item));
      setSelected((current) => current?.id === order.id ? updated : current);
      setToast({ type: "success", text: `Order berhasil diubah menjadi ${statusLabels[target]}.` });
      void loadOrders();
    } catch (err) {
      setToast({ type: "error", text: err instanceof Error ? err.message : "Gagal mengubah status order." });
    } finally {
      setBusyId(null);
    }
  };

  const requestCancel = (order: BookingOrder) => {
    const status = normalizeStatus(order.booking_status);
    if (status === "completed" || status === "cancelled") return;
    setCancelCandidate(order);
  };

  const handleCancel = async () => {
    const order = cancelCandidate;
    if (!order) return;
    setBusyId(order.id);
    try {
      const updated = await updateOrderStatus(order.id, "cancelled");
      setOrders((current) => current.map((item) => item.id === order.id ? updated : item));
      setSelected((current) => current?.id === order.id ? updated : current);
      setCancelCandidate(null);
      setToast({ type: "success", text: "Order berhasil dibatalkan." });
      void loadOrders();
    } catch (err) {
      setToast({ type: "error", text: err instanceof Error ? err.message : "Gagal membatalkan order." });
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="min-h-screen px-0 pb-8 text-[#161a23] sm:px-2 lg:px-0">
      <div className="mx-auto max-w-[1180px]">
        <div className="flex flex-col justify-between gap-6 md:flex-row md:items-end">
          <div>
            <div className="mb-3 inline-flex items-center gap-2 rounded-full border border-[#f3d5d9] bg-white/90 px-3 py-1.5 text-[10px] font-extrabold uppercase tracking-[0.13em] text-[#b10f1c] shadow-sm">
              <PackageCheck size={14} /> Order management
            </div>
            <h1 className="text-4xl font-extrabold tracking-[-0.04em] text-[#111827] md:text-[54px]">
              Pesanan pelanggan
            </h1>
            <p className="mt-3 text-lg font-medium text-[#7b7f8c]">
              Prioritaskan pesanan baru, hubungi pelanggan, lalu perbarui progres perjalanan.
            </p>
          </div>
          <div className="flex w-full max-w-md gap-2">
            <label className="flex h-11 min-w-0 flex-1 items-center gap-3 rounded-xl border border-[#e4e7f2] bg-white px-4 text-[#606473] shadow-sm transition focus-within:border-[#c1121f] focus-within:ring-4 focus-within:ring-[#c1121f]/10">
              <Search size={18} />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Cari ID, pelanggan, atau paket" className="min-w-0 flex-1 bg-transparent text-sm font-medium outline-none placeholder:text-[#8d91a0]" />
            </label>
            <button type="button" onClick={() => { setLoading(true); void loadOrders(); }} disabled={loading} className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-[#e4e7f2] bg-white text-[#535762] shadow-sm transition hover:border-[#c1121f] hover:text-[#c1121f] disabled:cursor-not-allowed disabled:opacity-60" aria-label="Muat ulang orders" title="Muat ulang orders">
              <RefreshCw size={17} className={loading ? "animate-spin" : undefined} />
            </button>
          </div>
        </div>

        <div className="mt-8 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <StatsCard title="Perlu tindakan" value={stats.pending} note={`${stats.todayNew} pesanan masuk hari ini`} icon={<AlertCircle size={16} />} prominent />
          <StatsCard title="Sedang diproses" value={stats.processing} note="Menunggu tindak lanjut" icon={<LoaderCircle size={16} />} />
          <StatsCard title="Terkonfirmasi" value={stats.confirmed} note="Siap masuk jadwal" icon={<CheckCircle2 size={16} />} />
          <StatsCard title="Selesai" value={stats.completed} note="Total perjalanan selesai" icon={<PackageCheck size={16} />} />
        </div>

        <div className="mt-12 flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
          <div className="flex items-center gap-2 text-sm font-bold text-[#535762]">
            <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-[#eef0ff] text-[#273148]"><SlidersHorizontal size={16} /></span>
            Filter status
          </div>
          <div className="flex min-w-0 max-w-full overflow-x-auto rounded-xl bg-white p-1 shadow-[0_12px_26px_-20px_rgba(17,24,39,0.7)] ring-1 ring-[#e4e7f2]">
            {filters.map((item) => (
              <button
                key={item.value}
                type="button"
                onClick={() => setFilter(item.value)}
                className={cn(
                  "inline-flex h-9 shrink-0 items-center gap-2 rounded-lg px-4 text-xs font-bold transition",
                  filter === item.value
                    ? "bg-[#111827] text-white shadow-sm"
                    : "text-[#6b7280] hover:text-[#111827]"
                )}
              >
                {item.label}
                <span className={cn("rounded-md px-1.5 py-0.5 text-[10px]", filter === item.value ? "bg-white/15" : "bg-[#f4f5f8] text-[#7b7f8c]")}>{item.value === "all" ? orders.length : orders.filter((order) => normalizeStatus(order.booking_status) === item.value).length}</span>
              </button>
            ))}
          </div>
        </div>

        <div className="mt-5 flex flex-wrap items-center justify-between gap-3 border-y border-[#eceef5] py-3 text-sm">
          <p className="font-medium text-[#707584]">
            Menampilkan <span className="font-extrabold text-[#111827]">{filteredOrders.length}</span> dari <span className="font-extrabold text-[#111827]">{orders.length}</span> pesanan. Urutan prioritas tertinggi lebih dulu.
          </p>
          {(query || filter !== "all") && (
            <button type="button" onClick={() => { setQuery(""); setFilter("all"); }} className="inline-flex items-center gap-2 text-xs font-extrabold text-[#b10f1c] transition hover:text-[#7f0b14]">
              <FilterX size={14} /> Reset filter
            </button>
          )}
        </div>

        {loading ? (
          <div className="mt-12 grid gap-5 lg:grid-cols-2"><Skeleton /><Skeleton /><Skeleton /><Skeleton /></div>
        ) : error ? (
          <div className="mt-12 rounded-2xl border border-rose-100 bg-rose-50/60 p-8 text-center">
            <XCircle className="mx-auto text-rose-600" size={28} />
            <p className="mt-3 text-sm font-bold text-rose-800">Orders belum dapat dimuat</p>
            <p className="mx-auto mt-1 max-w-lg text-sm text-rose-700">{error}</p>
            <button type="button" onClick={() => { setLoading(true); void loadOrders(); }} className="mt-5 inline-flex h-10 items-center gap-2 rounded-xl bg-[#111827] px-4 text-xs font-extrabold text-white hover:bg-[#273148]"><RefreshCw size={14} /> Coba lagi</button>
          </div>
        ) : filteredOrders.length === 0 ? (
          <div className="mt-12 flex min-h-[520px] items-center justify-center rounded-2xl border border-dashed border-[#e4e7f2] bg-[#fdfbff] text-center">
            <div>
              <div className="mx-auto flex h-16 w-16 items-center justify-center rounded-full bg-[#eef0ff] text-[#c1121f]">
                <PackageCheck size={26} />
              </div>
              <h2 className="mt-6 text-3xl font-extrabold tracking-[-0.04em]">{orders.length ? "Tidak ada hasil sesuai filter" : "Belum ada pesanan"}</h2>
              <p className="mx-auto mt-3 max-w-md text-base font-medium text-[#777c88]">{orders.length ? "Ubah kata kunci atau reset filter untuk melihat pesanan lain." : "Pesanan baru akan muncul otomatis setelah pelanggan memilih paket perjalanan."}</p>
              {orders.length > 0 && <button type="button" onClick={() => { setQuery(""); setFilter("all"); }} className="mt-6 h-10 rounded-xl bg-[#111827] px-4 text-xs font-extrabold text-white">Reset filter</button>}
            </div>
          </div>
        ) : (
          <div className="mt-12 grid gap-5 lg:grid-cols-2">
            {filteredOrders.map((order) => <OrderCard key={order.id} order={order} busy={busyId === order.id} onView={setSelected} onAdvance={handleAdvance} onCancel={requestCancel} />)}
          </div>
        )}
      </div>

      <DetailDrawer order={selected} onClose={() => setSelected(null)} onAdvance={handleAdvance} onCancel={requestCancel} busy={Boolean(selected && busyId === selected.id)} />
      <ToastNotification toast={toast} onClose={() => setToast(null)} />
      {cancelCandidate && (
        <ConfirmModal
          open
          title="Batalkan pesanan?"
          description={`Pesanan #${cancelCandidate.id.slice(0, 8)} akan ditandai sebagai dibatalkan. Aksi ini tidak dapat diurungkan.`}
          confirmLabel="Batalkan pesanan"
          cancelLabel="Kembali"
          variant="danger"
          loading={busyId === cancelCandidate.id}
          onConfirm={() => void handleCancel()}
          onCancel={() => setCancelCandidate(null)}
        />
      )}
    </div>
  );
}
