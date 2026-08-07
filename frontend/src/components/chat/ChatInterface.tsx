"use client";

import {
  FormEvent,
  memo,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  CalendarDays,
  CheckCircle2,
  MapPin,
  Plus,
  Send,
  Ticket,
  Utensils,
  X,
} from "lucide-react";
import RecommendationCard from "../cards/RecommendationCard";
import { TripPriceBlock } from "../pricing/TripPriceBlock";
import {
  apiFetch,
  assetURL,
  streamChat,
  TripPackage,
  GuestChatHistoryResponse,
} from "@/lib/api";
import { getTripAdultPrice, getTripChildPrice } from "@/lib/format";

type ChatMessage = {
  role: "user" | "assistant";
  content: string;
  workflow?: Record<string, unknown>[];
  packages?: TripPackage[];
  showRecommendations?: boolean;
  recommendationReason?: "initial" | "alternative" | "";
  shouldAnimate?: boolean;
  // PERF-1: while streaming, the assistant message is appended incrementally.
  // `streaming` shows a caret and suppresses the post-stream typing animation
  // (the text already appeared token-by-token, so animating again would
  // re-type the whole message).
  streaming?: boolean;
};

export default function ChatInterface() {
  const [prompt, setPrompt] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([
    {
      role: "assistant",
      content:
        "Halo, saya Vero Travel. Ceritakan destinasi, budget, durasi, dan gaya perjalanan yang Anda inginkan.",
    },
  ]);
  const [selectedPackage, setSelectedPackage] = useState<TripPackage | null>(null);
  const [completedTyping, setCompletedTyping] = useState<Record<number, boolean>>({
    0: true,
  });
  const [loading, setLoading] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  // PERF-1: AbortController for the in-flight streaming chat request so the
  // user can cancel a slow/long generation (and navigations abort cleanly).
  const streamAbortRef = useRef<AbortController | null>(null);

  // Keep a mutable copy of messages so stream callbacks don't close over the
  // stale array, avoiding the need to recreate callbacks on every render.
  const messagesRef = useRef(messages);
  messagesRef.current = messages;

  useEffect(() => {
    let cancelled = false;
    void apiFetch<GuestChatHistoryResponse>("/api/v1/chat/history")
      .then((data) => {
        if (cancelled || data.messages.length === 0) {
          return;
        }
        const nextMessages = data.messages.map((message) => ({
          role: message.role,
          content: message.content,
          shouldAnimate: false,
        }));
        setMessages(nextMessages);
        setCompletedTyping(
          Object.fromEntries(data.messages.map((_, index) => [index, true]))
        );
      })
      .catch(() => {
        // A missing/expired guest cookie simply starts a fresh chat.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Abort any in-flight stream when the component unmounts so the fetch does
  // not continue after the UI is gone.
  useEffect(() => {
    return () => {
      streamAbortRef.current?.abort();
    };
  }, []);

  const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
    messagesEndRef.current?.scrollIntoView({ behavior, block: "end" });
  }, []);

  // Scroll on loading state changes and after messages finalize; avoid
  // scrolling on every message array update during streaming to keep rendering
  // cheap. onDelta performs its own auto-scroll.
  useEffect(() => {
    scrollToBottom("auto");
  }, [loading, scrollToBottom]);

  const handleSubmit = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      const text = prompt.trim();
      if (!text || loading) {
        return;
      }
      setPrompt("");
      setLoading(true);

      setMessages((items) => {
        const nextUserIndex = items.length;
        setCompletedTyping((prev) => ({ ...prev, [nextUserIndex]: true }));
        return [...items, { role: "user" as const, content: text }];
      });

      // PERF-1: stream the assistant response. The assistant message is added
      // incrementally as deltas arrive so we don't show an empty chat bubble
      // while the model is still thinking.
      const abort = new AbortController();
      streamAbortRef.current = abort;

      let assistantIndex = -1;

      try {
        await streamChat(
          "/api/v1/chat",
          { prompt: text, stream: true },
          {
            onDelta: (fragment) => {
              setMessages((items) => {
                const last = items[items.length - 1];
                if (!last || last.role !== "assistant" || !last.streaming) {
                  const next = [
                    ...items,
                    { role: "assistant" as const, content: fragment, streaming: true },
                  ];
                  assistantIndex = items.length;
                  return next;
                }
                // Mutate in place via a new last object to keep the update O(1)
                // and avoid re-rendering unchanged sibling messages.
                const updated = { ...last, content: last.content + fragment };
                return [...items.slice(0, -1), updated];
              });
              scrollToBottom("auto");
            },
            onDone: (result) => {
              setMessages((items) => {
                const last = items[items.length - 1];
                // If deltas never arrived (empty stream / local fallback),
                // seed the content from the final result message.
                const wasStreaming = last?.streaming === true;
                const content = wasStreaming
                  ? (last.content || result.message)
                  : result.message;
                const newMsg: ChatMessage = {
                  role: "assistant",
                  content,
                  packages: result.recommended_packages ?? [],
                  showRecommendations: result.show_recommendations,
                  recommendationReason: result.recommendation_reason,
                  workflow: result.workflow,
                  streaming: false,
                  // PERF-1 fallback: if no deltas were received (streaming
                  // failed or was buffered), animate the text so the user
                  // still sees a ChatGPT-style typing effect instead of the
                  // full block appearing instantaneously.
                  shouldAnimate: !wasStreaming,
                };
                if (wasStreaming) {
                  return [...items.slice(0, -1), newMsg];
                }
                assistantIndex = items.length;
                return [...items, newMsg];
              });
              // Mark the finalized assistant message as done typing so the
              // recommendations block can render (it gates on completedTyping).
              setCompletedTyping((items) => ({
                ...items,
                [assistantIndex]: true,
              }));
            },
            onError: (message) => {
              setMessages((items) => {
                const last = items[items.length - 1];
                // If we already streamed partial text, keep it and append the
                // error notice; otherwise replace the empty placeholder.
                if (last?.streaming) {
                  if (last.content) {
                    return [
                      ...items.slice(0, -1),
                      { ...last, content: last.content + "\n\n" + message, streaming: false },
                    ];
                  }
                  return [...items.slice(0, -1), { role: "assistant" as const, content: message }];
                }
                return [...items, { role: "assistant" as const, content: message }];
              });
            },
          },
          { signal: abort.signal }
        );
      } finally {
        streamAbortRef.current = null;
        setLoading(false);
      }
    },
    [loading, prompt, scrollToBottom]
  );

  return (
    <div className="flex h-screen bg-[#fafafc]">
      <div
        className={`relative flex h-screen flex-col transition-all duration-300 ${
          selectedPackage ? "w-[65%]" : "w-full"
        }`}
      >
      <div className="flex-1 overflow-y-auto px-8 py-10 pb-32">
        <div className={`${selectedPackage ? "max-w-3xl" : "max-w-4xl"} mx-auto space-y-8`}>
          {messages.map((message, index) =>
            message.role === "user" ? (
              <div key={index} className="flex justify-end">
                <div className="bg-[#f0e8e8] text-slate-800 px-6 py-4 rounded-2xl rounded-tr-sm max-w-[80%] shadow-sm">
                  <p className="text-[15px] leading-relaxed">{message.content}</p>
                </div>
              </div>
            ) : (
              <AssistantMessage
                key={index}
                index={index}
                message={message}
                completedTyping={completedTyping[index]}
                onSelectPackage={setSelectedPackage}
                scrollToBottom={scrollToBottom}
                onTypingDone={setCompletedTyping}
              />
            )
          )}

          {loading && (
              <div className="flex items-center gap-3 mt-6 p-4 bg-white/50 border border-slate-100 rounded-2xl shadow-sm w-max">
                <div className="flex gap-1.5">
                  <div className="w-2 h-2 rounded-full bg-[#df3333] animate-bounce" style={{ animationDelay: "0ms" }}></div>
                  <div className="w-2 h-2 rounded-full bg-[#df3333] animate-bounce" style={{ animationDelay: "150ms" }}></div>
                  <div className="w-2 h-2 rounded-full bg-[#df3333] animate-bounce" style={{ animationDelay: "300ms" }}></div>
                </div>
                <span className="text-sm font-medium text-slate-500 italic flex items-center gap-2">
                  <span className="animate-pulse">Thinking</span>
                </span>
              </div>
          )}
          <div ref={messagesEndRef} className="h-24" />
        </div>
      </div>

      {/* Sticky Input Area */}
      <div className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-[#fafafc] via-[#fafafc] to-transparent pt-10 pb-8 px-8">
        <div className="max-w-4xl mx-auto">
          <form onSubmit={handleSubmit} className="bg-white border border-slate-200 rounded-full shadow-[0_4px_20px_-10px_rgba(0,0,0,0.1)] flex items-center p-2 pl-4">
            <button type="button" className="p-2 text-slate-400 hover:text-slate-600 transition-colors">
              <Plus size={20} />
            </button>
            <input
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              type="text"
              disabled={loading}
              placeholder="Ask Vero about Japan..."
              className="flex-1 bg-transparent border-none outline-none px-3 text-[15px] text-slate-700 placeholder:text-slate-400 disabled:opacity-60"
            />
            <button type="submit" disabled={loading || !prompt.trim()} className="bg-[#df3333] hover:bg-[#c92a2a] disabled:opacity-60 text-white p-3 rounded-full transition-colors shadow-md flex items-center justify-center">
              <Send size={18} className="ml-0.5" />
            </button>
          </form>
          <p className="text-center text-[11px] text-slate-400 mt-4 font-medium">
            Vero AI can make mistakes. Consider verifying important information.
          </p>
        </div>
      </div>
      </div>
      {selectedPackage && (
        <PackageDetailPanel
          trip={selectedPackage}
          messages={messages}
          onClose={() => setSelectedPackage(null)}
        />
      )}
    </div>
  );
}

type AssistantMessageProps = {
  index: number;
  message: ChatMessage;
  completedTyping: boolean;
  onSelectPackage: (trip: TripPackage) => void;
  scrollToBottom: (behavior?: ScrollBehavior) => void;
  onTypingDone: React.Dispatch<React.SetStateAction<Record<number, boolean>>>;
};

const AssistantMessage = memo(function AssistantMessage({
  index,
  message,
  completedTyping,
  onSelectPackage,
  scrollToBottom,
  onTypingDone,
}: AssistantMessageProps) {
  const handleTypingDone = useCallback(() => {
    onTypingDone((items) => ({ ...items, [index]: true }));
  }, [onTypingDone, index]);

  return (
    <div className="flex items-start gap-4">
      <div className="w-8 h-8 rounded-full bg-[#df3333] flex items-center justify-center shrink-0 shadow-md">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
          <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </div>
      <div className="flex-1 space-y-6">
        <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">Vero Travel</span>
        <div className="bg-white border border-slate-100 shadow-sm rounded-2xl rounded-tl-sm p-6 text-slate-700 leading-relaxed text-[15px]">
          {message.streaming ? (
            // PERF-1: text arrives incrementally via SSE; show a caret
            // while streaming instead of the post-stream typing anim.
            <p className="whitespace-pre-wrap">
              {message.content}
              <span className="ml-0.5 inline-block h-4 w-1 animate-pulse rounded bg-[#df3333] align-[-2px]" />
            </p>
          ) : message.shouldAnimate ? (
            <TypingText
              text={message.content}
              onUpdate={scrollToBottom}
              onDone={handleTypingDone}
            />
          ) : (
            <p className="whitespace-pre-wrap">{message.content}</p>
          )}
        </div>
        {message.showRecommendations &&
          message.packages &&
          message.packages.length > 0 &&
          completedTyping && (
            <PackageRecommendations
              packages={message.packages}
              reason={message.recommendationReason}
              onSelect={onSelectPackage}
            />
          )}
      </div>
    </div>
  );
});

function TypingText({
  text,
  onUpdate,
  onDone,
}: {
  text: string;
  onUpdate?: () => void;
  onDone: () => void;
}) {
  const [visibleLength, setVisibleLength] = useState(0);
  const doneRef = useRef(false);
  const onDoneRef = useRef(onDone);
  const onUpdateRef = useRef(onUpdate);
  const charsPerTick = text.length > 500 ? 4 : 2;

  useEffect(() => {
    onDoneRef.current = onDone;
  }, [onDone]);

  useEffect(() => {
    onUpdateRef.current = onUpdate;
  }, [onUpdate]);

  useEffect(() => {
    setVisibleLength(0);
    doneRef.current = false;
  }, [text]);

  useEffect(() => {
    if (visibleLength >= text.length) {
      if (!doneRef.current) {
        doneRef.current = true;
        onDoneRef.current();
      }
      return;
    }
    const timer = window.setTimeout(() => {
      setVisibleLength((current) => Math.min(current + charsPerTick, text.length));
    }, 16);
    return () => window.clearTimeout(timer);
  }, [charsPerTick, text.length, visibleLength]);

  // Drive scroll from a separate effect so the typing effect itself doesn't
  // re-render on every scroll callback identity change.
  useEffect(() => {
    onUpdateRef.current?.();
  }, [visibleLength]);

  return (
    <p className="whitespace-pre-wrap">
      {text.slice(0, visibleLength)}
      {visibleLength < text.length && (
        <span className="ml-0.5 inline-block h-4 w-1 animate-pulse rounded bg-[#df3333] align-[-2px]" />
      )}
    </p>
  );
}

function PackageRecommendations({
  packages,
  reason,
  onSelect,
}: {
  packages: TripPackage[];
  reason?: "initial" | "alternative" | "";
  onSelect: (trip: TripPackage) => void;
}) {
  const heading =
    reason === "alternative"
      ? "Alternatif paket lain dari Vero"
      : "Paket yang direkomendasikan Vero";
  return (
    <div className="space-y-4">
      <h2 className="text-sm font-bold uppercase tracking-wider text-slate-500">
        {heading}
      </h2>
      <div className="grid grid-cols-1 gap-5 md:grid-cols-3">
        {packages.map((trip) => (
          <RecommendationCard
            key={trip.id}
            title={trip.title}
            description={trip.summary || trip.overview || trip.destination}
            category={trip.category}
            image={assetURL(trip.image_url || trip.media?.[0]?.url)}
            icon={<Utensils size={14} className="text-[#df3333]" />}
            onSelect={() => onSelect(trip)}
          />
        ))}
      </div>
    </div>
  );
}

function PackageDetailPanel({
  trip,
	messages,
  onClose,
}: {
  trip: TripPackage;
	messages: ChatMessage[];
  onClose: () => void;
}) {
  const image = assetURL(trip.image_url || trip.media?.[0]?.url);
  const adultPrice = getTripAdultPrice(trip);
  const childPrice = getTripChildPrice(trip);

	// Extract draft or created order state from the AI workflow payloads
	let draftPaxAdult = 1;
	let draftPaxChild = 0;
	let draftDate = "Flexible";
	let isOrderCreated = false;
	let orderId = "";

	if (messages) {
		for (const msg of messages) {
			if (msg.role === "assistant" && msg.workflow) {
				for (const wf of msg.workflow) {
					if (wf.tool === "update_order_draft" && wf.data && typeof wf.data === "object") {
						const data = wf.data as Record<string, unknown>;
						if (data.trip_id === trip.id) {
							draftPaxAdult = Number(data.adult_pax) || 1;
							draftPaxChild = Number(data.child_pax) || 0;
							if (data.travel_date) draftDate = String(data.travel_date);
						}
					}
					if (wf.tool === "create_booking" && wf.status === "success" && wf.data && typeof wf.data === "object") {
						const data = wf.data as Record<string, unknown>;
						isOrderCreated = true;
						orderId = String(data.booking_id);
					}
				}
			}
		}
	}

	const estimatedTotal = (adultPrice.displayPrice * draftPaxAdult) + (childPrice.displayPrice * draftPaxChild);
	const paxLabel = `${draftPaxAdult} Dewasa${draftPaxChild > 0 ? `, ${draftPaxChild} Anak` : ""}`;

  return (
    <aside className="h-screen w-[35%] overflow-y-auto border-l border-slate-200 bg-white shadow-[-20px_0_60px_-45px_rgba(15,23,42,0.55)]">
      <div className="sticky top-0 z-10 flex items-center justify-between border-b border-slate-100 bg-white/90 px-6 py-4 backdrop-blur">
        <div>
          <p className="text-xs font-bold uppercase tracking-wider text-[#df3333]">
            Detail Paket
          </p>
          <h2 className="text-xl font-black tracking-tight text-slate-900">
            {trip.title}
          </h2>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-full bg-slate-100 p-2 text-slate-500"
          aria-label="Close package detail"
        >
          <X size={18} />
        </button>
      </div>

      <div className="p-6">
        <div className="relative h-56 overflow-hidden rounded-3xl bg-slate-200">
          <div
            className="absolute inset-0 bg-cover bg-center"
            style={{
              backgroundImage: image
                ? `url(${image})`
                : "linear-gradient(135deg,#111827,#df3333)",
            }}
          />
          <div className="absolute inset-0 bg-gradient-to-t from-black/60 to-transparent" />
          <div className="absolute bottom-5 left-5 right-5 text-white">
            <div className="mb-2 flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-white/80">
              <MapPin size={14} />
              {trip.location || trip.destination}
            </div>
            <div className="text-3xl font-black">{trip.title}</div>
          </div>
        </div>

        <div className="mt-6 grid grid-cols-2 gap-3">
          <InfoPill icon={<CalendarDays size={16} />} label={draftDate !== "Flexible" ? draftDate : trip.duration || "Flexible"} />
          <InfoPill icon={<Ticket size={16} />} label={paxLabel} />
        </div>

        <section className="mt-7 rounded-3xl border border-slate-100 bg-slate-50 p-5">
          <TripPriceBlock label="Dewasa" price={adultPrice} />
          {childPrice.displayPrice > 0 ? (
            <div className="mt-4 border-t border-slate-200 pt-4">
              <TripPriceBlock label="Harga Anak" price={childPrice} size="md" />
            </div>
          ) : null}
					<div className="mt-4 border-t border-slate-200 pt-4">
						<div className="flex justify-between items-center text-slate-800">
							<span className="font-bold">Estimasi Total</span>
							<span className="text-xl font-black">
								{new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", maximumFractionDigits: 0 }).format(estimatedTotal)}
							</span>
						</div>
					</div>
        </section>

				{isOrderCreated ? (
					<section className="mt-7 rounded-3xl border border-emerald-100 bg-emerald-50 p-5">
						<div className="flex items-center gap-3">
							<CheckCircle2 size={24} className="text-emerald-500" />
							<h3 className="text-lg font-black text-emerald-900">Order Berhasil</h3>
						</div>
						<p className="mt-2 text-sm leading-6 text-emerald-800 font-medium">
							ID Pesanan: {orderId.slice(0, 8)}<br />
							Tim kami akan menghubungi Anda melalui kontak yang telah diberikan untuk membantu proses selanjutnya.
						</p>
					</section>
				) : null}

        <section className="mt-7">
          <h3 className="text-lg font-black text-slate-900">Summary</h3>
          <p className="mt-3 text-sm leading-7 text-slate-600">
            {trip.summary || trip.overview || "Paket ini dibuat dari backoffice TravelOS."}
          </p>
        </section>

        {trip.highlights?.length ? (
          <section className="mt-7">
            <h3 className="text-lg font-black text-slate-900">Highlights</h3>
            <div className="mt-3 flex flex-wrap gap-2">
              {trip.highlights.map((highlight) => (
                <span
                  key={highlight}
                  className="rounded-full bg-[#f2e7e7] px-3 py-1.5 text-xs font-bold text-[#8e2929]"
                >
                  {highlight}
                </span>
              ))}
            </div>
          </section>
        ) : null}

        {trip.itineraries?.length ? (
          <section className="mt-7">
            <h3 className="text-lg font-black text-slate-900">Itinerary</h3>
            <div className="mt-4 space-y-4">
              {trip.itineraries.map((item) => (
                <div key={`${item.day}-${item.title}`} className="rounded-2xl border border-slate-100 p-4">
                  <div className="text-xs font-black uppercase text-[#df3333]">
                    Day {item.day}
                  </div>
                  <div className="mt-1 font-bold text-slate-900">{item.title}</div>
                  <p className="mt-2 text-sm leading-6 text-slate-500">
                    {item.description}
                  </p>
                </div>
              ))}
            </div>
          </section>
        ) : null}

        {(trip.amenities_included?.length || trip.amenities_excluded?.length) ? (
          <section className="mt-7">
            <h3 className="text-lg font-black text-slate-900">Fasilitas Paket</h3>
            <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
              <AmenityColumn
                title="Termasuk"
                items={trip.amenities_included ?? []}
                tone="included"
              />
              <AmenityColumn
                title="Tidak Termasuk"
                items={trip.amenities_excluded ?? []}
                tone="excluded"
              />
            </div>
          </section>
        ) : null}
      </div>
    </aside>
  );
}

function InfoPill({ icon, label }: { icon: React.ReactNode; label: string }) {
  return (
    <div className="flex items-center gap-2 rounded-2xl bg-slate-50 p-4 text-sm font-bold text-slate-700">
      <span className="text-[#df3333]">{icon}</span>
      {label}
    </div>
  );
}

function AmenityColumn({
  title,
  items,
  tone,
}: {
  title: string;
  items: string[];
  tone: "included" | "excluded";
}) {
  const isIncluded = tone === "included";

  return (
    <div
      className={`rounded-2xl border p-4 ${
        isIncluded
          ? "border-emerald-100 bg-emerald-50/60"
          : "border-rose-100 bg-rose-50/60"
      }`}
    >
      <h4
        className={`text-sm font-black ${
          isIncluded ? "text-emerald-800" : "text-rose-800"
        }`}
      >
        {title}
      </h4>
      {items.length > 0 ? (
        <ul className="mt-3 space-y-2 text-sm leading-6 text-slate-600">
          {items.map((item) => (
            <li key={item} className="flex items-start gap-2">
              <span
                className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${
                  isIncluded ? "bg-emerald-500" : "bg-rose-500"
                }`}
              />
              <span>{item}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-3 text-sm text-slate-400">Belum ada informasi.</p>
      )}
    </div>
  );
}
