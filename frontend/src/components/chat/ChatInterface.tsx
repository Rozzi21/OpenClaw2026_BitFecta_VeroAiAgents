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
  id: string;
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

let messageIdCounter = 0;
function nextMessageId() {
  return `msg-${++messageIdCounter}`;
}

export default function ChatInterface() {
  const [prompt, setPrompt] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([
    {
      id: nextMessageId(),
      role: "assistant",
      content:
        "Halo, saya Vero Travel. Ceritakan destinasi, budget, durasi, dan gaya perjalanan yang Anda inginkan.",
    },
  ]);
  const [selectedPackage, setSelectedPackage] = useState<TripPackage | null>(null);
  const [completedTyping, setCompletedTyping] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  // PERF-1: AbortController for the in-flight streaming chat request so the
  // user can cancel a slow/long generation (and navigations abort cleanly).
  const streamAbortRef = useRef<AbortController | null>(null);

  // Keep a mutable copy of messages so stream callbacks don't close over the
  // stale array, avoiding the need to recreate callbacks on every render.
  const messagesRef = useRef(messages);
  messagesRef.current = messages;

  // Throttle scroll-to-bottom during streaming so high-frequency token deltas
  // do not queue many smooth-scroll animations or force layout thrashing.
  const scrollTickRef = useRef(false);
  const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
    if (behavior === "auto" && scrollTickRef.current) {
      return;
    }
    scrollTickRef.current = true;
    requestAnimationFrame(() => {
      messagesEndRef.current?.scrollIntoView({ behavior, block: "end" });
      scrollTickRef.current = false;
    });
  }, []);

  // Frame-based streaming scheduler state. Delta fragments are accumulated in
  // a mutable ref and flushed to React state at most once per animation frame.
  const streamStateRef = useRef<{
    active: boolean;
    buffer: string;
    assistantId: string | null;
    rafId: number | null;
  }>({ active: false, buffer: "", assistantId: null, rafId: null });

  const flushStreamBuffer = useCallback(() => {
    const state = streamStateRef.current;
    if (!state.active || state.assistantId === null) {
      state.buffer = "";
      state.rafId = null;
      return;
    }
    const buffered = state.buffer;
    if (buffered === "") {
      state.rafId = null;
      return;
    }
    state.buffer = "";
    setMessages((items) => {
      const targetIndex = items.findIndex((m) => m.id === state.assistantId);
      if (targetIndex === -1) {
        return items;
      }
      const target = items[targetIndex];
      if (!target.streaming) {
        return items;
      }
      const updated = { ...target, content: target.content + buffered };
      const next = [...items];
      next[targetIndex] = updated;
      return next;
    });
    scrollToBottom("auto");
    state.rafId = null;
  }, [scrollToBottom]);

  const scheduleStreamFlush = useCallback(() => {
    const state = streamStateRef.current;
    if (state.rafId !== null || !state.active) {
      return;
    }
    state.rafId = requestAnimationFrame(() => {
      flushStreamBuffer();
    });
  }, [flushStreamBuffer]);

  const stopStreamScheduler = useCallback(() => {
    const state = streamStateRef.current;
    if (state.rafId !== null) {
      cancelAnimationFrame(state.rafId);
      state.rafId = null;
    }
    state.active = false;
    state.buffer = "";
    state.assistantId = null;
  }, []);

  useEffect(() => {
    let cancelled = false;
    void apiFetch<GuestChatHistoryResponse>("/api/v1/chat/history")
      .then((data) => {
        if (cancelled || data.messages.length === 0) {
          return;
        }
        const nextMessages: ChatMessage[] = data.messages.map((message) => ({
          id: nextMessageId(),
          role: message.role,
          content: message.content,
          shouldAnimate: false,
        }));
        setMessages(nextMessages);
        setCompletedTyping(
          Object.fromEntries(nextMessages.map((m) => [m.id, true]))
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
      stopStreamScheduler();
    };
  }, [stopStreamScheduler]);

  // Scroll on loading state changes and after messages finalize; avoid
  // scrolling on every message array update during streaming to keep rendering
  // cheap. The scheduler performs its own auto-scroll on each frame flush.
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

      const userId = nextMessageId();
      setMessages((items) => [
        ...items,
        { id: userId, role: "user" as const, content: text },
      ]);
      setCompletedTyping((prev) => ({ ...prev, [userId]: true }));

      // PERF-1: stream the assistant response. The assistant message is added
      // incrementally as deltas arrive so we don't show an empty chat bubble
      // while the model is still thinking.
      const abort = new AbortController();
      streamAbortRef.current = abort;

      const assistantId = nextMessageId();
      streamStateRef.current = {
        active: true,
        buffer: "",
        assistantId,
        rafId: null,
      };

      // Seed the empty assistant message before the first delta so the UI has
      // a stable target to update during the frame scheduler.
      setMessages((items) => [
        ...items,
        { id: assistantId, role: "assistant" as const, content: "", streaming: true },
      ]);

      try {
        await streamChat(
          "/api/v1/chat",
          { prompt: text, stream: true },
          {
            onDelta: (fragment) => {
              const state = streamStateRef.current;
              if (!state.active || state.assistantId !== assistantId) {
                return;
              }
              state.buffer += fragment;
              scheduleStreamFlush();
            },
            onDone: (result) => {
              // Flush the scheduler tail: deltas that arrived after the last
              // animation frame are still sitting in the buffer. Merge them
              // into this final setMessages so no trailing text is lost.
              const pending = streamStateRef.current.buffer;
              stopStreamScheduler();
              setMessages((items) => {
                const targetIndex = items.findIndex((m) => m.id === assistantId);
                const target = targetIndex !== -1 ? items[targetIndex] : null;
                const wasStreaming = target?.streaming === true;
                const content = wasStreaming
                  ? (target.content + pending || result.message)
                  : result.message;
                const newMsg: ChatMessage = {
                  id: assistantId,
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
                  const next = [...items];
                  next[targetIndex] = newMsg;
                  return next;
                }
                return [...items, newMsg];
              });
              // Mark the finalized assistant message as done typing so the
              // recommendations block can render (it gates on completedTyping).
              setCompletedTyping((items) => ({
                ...items,
                [assistantId]: true,
              }));
            },
            onError: (message) => {
              // Same tail-flush as onDone: keep buffered deltas that never
              // reached a frame, then surface the error below them.
              const pending = streamStateRef.current.buffer;
              stopStreamScheduler();
              setMessages((items) => {
                const targetIndex = items.findIndex((m) => m.id === assistantId);
                const target = targetIndex !== -1 ? items[targetIndex] : null;
                // If we already streamed partial text, keep it and append the
                // error notice; otherwise replace the empty placeholder.
                if (target?.streaming) {
                  const partial = target.content + pending;
                  if (partial) {
                    const next = [...items];
                    next[targetIndex] = {
                      ...target,
                      content: partial + "\n\n" + message,
                      streaming: false,
                    };
                    return next;
                  }
                  const next = [...items];
                  next[targetIndex] = {
                    id: assistantId,
                    role: "assistant",
                    content: message,
                  };
                  return next;
                }
                return [...items, { id: assistantId, role: "assistant", content: message }];
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
    [loading, prompt, scheduleStreamFlush, stopStreamScheduler]
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
          {messages.map((message) =>
            message.role === "user" ? (
              <div key={message.id} className="flex justify-end">
                <div className="bg-[#f0e8e8] text-slate-800 px-6 py-4 rounded-2xl rounded-tr-sm max-w-[80%] shadow-sm">
                  <p className="text-[15px] leading-relaxed">{message.content}</p>
                </div>
              </div>
            ) : (
              // PERF-1: while a streaming message is still empty (model is
              // thinking, no content delta arrived yet) hide the bubble — the
              // "Thinking" dots below already indicate work in progress.
              // Rendering an empty bubble with a caret looked broken.
              (message.content || !message.streaming) && (
                <AssistantMessage
                  key={message.id}
                  id={message.id}
                  message={message}
                  completedTyping={completedTyping[message.id]}
                  onSelectPackage={setSelectedPackage}
                  scrollToBottom={scrollToBottom}
                  onTypingDone={setCompletedTyping}
                />
              )
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
  id: string;
  message: ChatMessage;
  completedTyping: boolean;
  onSelectPackage: (trip: TripPackage) => void;
  scrollToBottom: (behavior?: ScrollBehavior) => void;
  onTypingDone: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
};

const AssistantMessage = memo(function AssistantMessage({
  id,
  message,
  completedTyping,
  onSelectPackage,
  scrollToBottom,
  onTypingDone,
}: AssistantMessageProps) {
  const handleTypingDone = useCallback(() => {
    onTypingDone((items) => ({ ...items, [id]: true }));
  }, [onTypingDone, id]);

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
