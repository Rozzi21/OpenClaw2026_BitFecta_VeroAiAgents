"use client";

import Link from "next/link";
import { Suspense, useEffect, useRef, useState } from "react";
import { ArrowLeft, MapPin, Clock, CheckCircle2, Plane, BedDouble, Ticket, ShieldCheck } from "lucide-react";
import { APIError, apiFetch, assetURL, BookingOrder, ensureCustomerSession, TripPackage } from "@/lib/api";
import { getTripAdultPrice, getTripChildPrice } from "@/lib/format";
import { TripPriceBlock, TripPriceInline } from "@/components/pricing/TripPriceBlock";
import { GoogleButton } from "@/components/auth/GoogleButton";
import { OAuthReceiver } from "@/components/auth/OAuthReceiver";

export default function TripDetailPage({ params }: { params: { id: string } }) {
  const [trip, setTrip] = useState<TripPackage | null>(null);
  const [creatingOrder, setCreatingOrder] = useState(false);
  const [order, setOrder] = useState<BookingOrder | null>(null);
  const [orderError, setOrderError] = useState<string | null>(null);
  const [authRequired, setAuthRequired] = useState(false);
  const [contact, setContact] = useState("");
  const idempotencyKeyRef = useRef<string | null>(null);

  useEffect(() => {
    apiFetch<TripPackage>(`/api/v1/packages/${params.id}`)
      .then(setTrip)
      .catch(() => setTrip(null));
  }, [params.id]);

  if (!trip) {
    return (
      <div className="flex h-screen items-center justify-center bg-slate-50 text-slate-500">
        Loading trip package...
      </div>
    );
  }

  const image = assetURL(trip.image_url || trip.media?.[0]?.url);
  const adultPrice = getTripAdultPrice(trip);
  const childPrice = getTripChildPrice(trip);

  async function handleConfirmOrder() {
    if (!trip || creatingOrder || order) {
      return;
    }
    const contactValue = contact.trim();
    // POST /api/v1/orders anchors the one-order guest entitlement on the
    // normalized contact, so the backend rejects a contact it cannot anchor
    // (400 BOOKING_VALIDATION_FAILED). Check the same shape here to give a
    // useful message instead of a generic validation error.
    const isEmail = contactValue.includes("@");
    const hasDigits = /\d/.test(contactValue);
    setCreatingOrder(true);
    setOrderError(null);
    setAuthRequired(false);
    try {
      if (!isEmail && !hasDigits) {
        setOrderError("Enter your email address or phone number so we can confirm this order.");
        return;
      }
	  idempotencyKeyRef.current ??= crypto.randomUUID();
	  // ensureCustomerSession renews the 15-minute access token from the
	  // refresh cookie; without it a signed-in user whose token expired would
	  // fall back to the guest endpoint and hit GUEST_ORDER_LIMIT_REACHED.
	  const authenticated = (await ensureCustomerSession()) === "active";
      const created = await apiFetch<BookingOrder>(authenticated ? "/api/v1/bookings" : "/api/v1/orders", {
        method: "POST",
		headers: { "Idempotency-Key": idempotencyKeyRef.current },
		body: JSON.stringify({
		  trip_id: trip.id,
		  adult_pax: 1,
		  child_pax: 0,
		  contact_name: "Guest",
		  contact_email: isEmail ? contactValue : "",
		  contact_phone: isEmail ? "" : contactValue,
		  travel_date: trip.package_start_date ?? new Date(Date.now() + 86400000).toISOString().slice(0, 10),
		}),
      });
      setOrder(created);
	  idempotencyKeyRef.current = null;
    } catch (error) {
	  if (error instanceof APIError && error.code === "GUEST_ORDER_LIMIT_REACHED") {
		setAuthRequired(true);
		setOrderError("Your guest order has already been used. Sign in to create another order.");
		return;
	  }
      setOrderError(
        error instanceof Error
          ? error.message
          : "Order belum bisa dibuat. Silakan coba lagi."
      );
    } finally {
      setCreatingOrder(false);
    }
  }

  return (
    <div className="flex-1 overflow-y-auto h-screen bg-slate-50">
      <Suspense fallback={null}>
        <OAuthReceiver />
      </Suspense>
      <div className="relative w-full h-[65vh] min-h-[400px] overflow-hidden rounded-b-[40px] shadow-lg">
        <div className="absolute inset-0 bg-gradient-to-t from-black/90 via-black/40 to-black/10 z-10" />
        <div
          className="absolute inset-0 bg-cover bg-center"
          style={{ backgroundImage: image ? `url(${image})` : "linear-gradient(135deg,#111827,#df3333)" }}
        />
        
        {/* Top bar overlay */}
        <div className="absolute top-0 left-0 right-0 p-8 z-20 flex justify-between items-center">
          <Link href="/" className="inline-flex items-center gap-2 px-4 py-2 bg-white/20 hover:bg-white/30 backdrop-blur-md text-white rounded-full text-sm font-medium transition-colors">
            <ArrowLeft size={16} />
            Back to Chat
          </Link>
        </div>

        {/* Hero Content */}
        <div className="absolute bottom-0 left-0 right-0 p-12 z-20 max-w-7xl mx-auto w-full flex flex-col justify-end h-full">
          <div className="inline-flex items-center gap-2 text-white/80 font-bold tracking-widest text-xs mb-3">
            <MapPin size={14} />
            {trip.location || trip.destination}
          </div>
          <h1 className="text-6xl md:text-8xl font-black text-white mb-6 tracking-tight">{trip.title}</h1>
          <p className="text-lg md:text-xl text-white/90 max-w-2xl leading-relaxed font-light">
            {trip.summary || trip.overview || "A curated journey powered by Vero Travel."}
          </p>
        </div>
      </div>

      <div className="max-w-7xl mx-auto px-8 md:px-12 py-12 flex flex-col lg:flex-row gap-12">
        
        {/* Main Content (Left) */}
        <div className="flex-1 space-y-12">
          
          <div className="flex flex-wrap gap-4">
            <div className="bg-[#f2e7e7] text-[#8e2929] px-5 py-3 rounded-full text-sm font-semibold flex items-center gap-2">
              <Clock size={16} /> Duration: {trip.duration || "Flexible"}
            </div>
            {trip.category && (
              <div className="bg-[#f2e7e7] text-[#8e2929] px-5 py-3 rounded-full text-sm font-semibold capitalize">
                {trip.category}
              </div>
            )}
          </div>

          {/* Highlights Section */}
          <section>
            <h2 className="text-3xl font-bold text-slate-900 mb-8">Highlights</h2>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
              {(trip.highlights?.length ? trip.highlights : ["Curated itinerary", "Local experience"]).map((highlight) => (
                <HighlightCard
                  key={highlight}
                  title={highlight}
                  description={trip.summary || "Personalized by Vero Travel."}
                  imgUrl={image}
                />
              ))}
            </div>
          </section>

          {/* AI Reasoning Panel */}
          <section className="bg-white p-8 rounded-[2rem] border border-slate-200/60 shadow-sm">
            <div className="flex items-center gap-3 mb-6">
              <div className="w-10 h-10 rounded-full bg-[#df3333] flex items-center justify-center shrink-0 shadow-md shadow-red-500/20">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
                </svg>
              </div>
              <h2 className="text-xl font-bold text-slate-800">Why Vero Recommends This</h2>
            </div>
            
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {(trip.amenities_included?.length ? trip.amenities_included : ["Curated package", "AI assisted planning"]).map((item) => (
                <ReasonItem key={item} text={item} />
              ))}
            </div>
          </section>

        </div>

        {/* Sidebar (Right) - Booking Widget */}
        <div className="w-full lg:w-[400px] shrink-0">
          <div className="bg-white border border-slate-200/60 shadow-xl shadow-slate-200/50 rounded-[2rem] p-8 sticky top-8">
            <h3 className="text-2xl font-bold text-slate-900 mb-6">Trip Overview</h3>
            
            <div className="space-y-4 mb-8">
              <CostRow
                icon={<Plane size={18} />}
                label="Package Price"
                amount={<TripPriceInline price={adultPrice} />}
              />
              {childPrice.displayPrice > 0 ? (
                <CostRow
                  icon={<BedDouble size={18} />}
                  label="Child Price"
                  amount={<TripPriceInline price={childPrice} />}
                />
              ) : null}
              <CostRow icon={<Ticket size={18} />} label="Activities" amount="Included" />
            </div>

            <div className="border-t border-slate-100 pt-6 mb-8">
              <TripPriceBlock label="Estimated Total" price={adultPrice} size="lg" />
            </div>

            <div className="space-y-3">
              <div className="space-y-2 text-left">
                <label htmlFor="order-contact" className="block text-sm font-bold text-slate-700">
                  Email or phone number
                </label>
                <input
                  id="order-contact"
                  type="text"
                  inputMode="email"
                  autoComplete="email"
                  value={contact}
                  onChange={(event) => setContact(event.target.value)}
                  disabled={Boolean(order)}
                  aria-describedby="order-contact-hint"
                  placeholder="you@example.com or 08123456789"
                  className="w-full rounded-xl border border-slate-200 px-4 py-3 text-slate-900 placeholder:text-slate-400 focus:border-[#df3333] focus:outline-none focus:ring-2 focus:ring-[#df3333]/30 disabled:bg-slate-50"
                />
                <p id="order-contact-hint" className="text-xs font-medium text-slate-500">
                  Required so our team can confirm your order. Guests may create one order per contact.
                </p>
              </div>
              <button
                type="button"
                onClick={handleConfirmOrder}
                disabled={creatingOrder || Boolean(order) || contact.trim() === ""}
                className="w-full bg-[#df3333] hover:bg-[#c92a2a] disabled:opacity-60 disabled:cursor-not-allowed text-white py-4 rounded-xl font-bold text-lg transition-colors shadow-lg shadow-red-500/20 flex items-center justify-center gap-2"
              >
                <Plane size={20} />
                {order ? "Order Saved" : creatingOrder ? "Saving Order..." : "Confirm & Create Order"}
              </button>
              {order ? (
				<div className="space-y-3 rounded-xl bg-emerald-50 p-4 text-sm font-semibold text-emerald-800">
				  <p>Your order has been created successfully.</p>
				  <p>You can continue tracking this order as a guest. To create another order, please sign in.</p>
				  <div className="flex flex-wrap gap-2">
					<Link href={`/order/${order.id}`} className="rounded-lg bg-emerald-700 px-3 py-2 text-white">Continue Tracking</Link>
					<Link href="/login" className="rounded-lg border border-emerald-700 px-3 py-2">Login</Link>
					<Link href="/register" className="rounded-lg border border-emerald-700 px-3 py-2">Register</Link>
				  </div>
				</div>
              ) : null}
              {orderError ? (
                <div className="rounded-xl bg-rose-50 p-3 text-sm font-semibold text-rose-700">
                  {orderError}
                </div>
              ) : null}
			  {authRequired ? (
				<div className="space-y-3 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
				  <p className="font-bold">Sign in to create another order.</p>
				  <div className="grid gap-2">
					<Suspense fallback={null}>
					  <GoogleButton />
					</Suspense>
					<Link href="/login" className="rounded-lg bg-[#df3333] px-3 py-2 text-center font-bold text-white">Login</Link>
					<Link href="/register" className="rounded-lg border border-amber-700 px-3 py-2 text-center font-bold">Create Account</Link>
				  </div>
				</div>
			  ) : null}
            </div>

            <div className="mt-6 flex items-center justify-center gap-2 text-xs text-slate-400 font-medium">
              <ShieldCheck size={14} />
              Manual admin processing. DOKU payment temporarily disabled.
            </div>
          </div>
        </div>

      </div>
    </div>
  );
}

function HighlightCard({ title, description, imgUrl }: { title: string; description: string; imgUrl: string }) {
  return (
    <div className="group relative h-72 rounded-3xl overflow-hidden cursor-pointer">
      <div className="absolute inset-0 bg-gradient-to-t from-black/90 via-black/30 to-transparent z-10" />
      <div 
        className="absolute inset-0 bg-cover bg-center transition-transform duration-700 group-hover:scale-110" 
        style={{ backgroundImage: imgUrl ? `url(${imgUrl})` : "linear-gradient(135deg,#111827,#df3333)" }}
      />
      <div className="absolute bottom-0 left-0 right-0 p-6 z-20">
        <h4 className="text-white text-2xl font-bold mb-2">{title}</h4>
        <p className="text-white/80 text-sm leading-relaxed">{description}</p>
      </div>
    </div>
  );
}

function ReasonItem({ text }: { text: string }) {
  return (
    <div className="flex items-start gap-3 bg-slate-50 p-4 rounded-xl border border-slate-100">
      <CheckCircle2 size={18} className="text-[#df3333] shrink-0 mt-0.5" />
      <span className="text-sm text-slate-700 font-medium leading-relaxed">{text}</span>
    </div>
  );
}

function CostRow({
  icon,
  label,
  amount,
}: {
  icon: React.ReactNode;
  label: string;
  amount: React.ReactNode;
}) {
  return (
    <div className="flex justify-between items-center gap-4 py-2">
      <div className="flex items-center gap-3 text-slate-600">
        <div className="w-8 h-8 rounded-full bg-slate-100 flex items-center justify-center text-slate-500">
          {icon}
        </div>
        <span className="font-medium">{label}</span>
      </div>
      <div className="text-right">{amount}</div>
    </div>
  );
}
