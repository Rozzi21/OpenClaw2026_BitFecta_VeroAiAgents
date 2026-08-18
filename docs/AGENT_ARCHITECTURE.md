# Vero AI Travel Agent Architecture

Status: **proposal desain Phase 2 — belum diimplementasikan**  
Tanggal: 18 Agustus 2026

Dokumen ini mendefinisikan evolusi Vero dari chatbot dengan function calling
menjadi **AI Travel Agent production-grade**. Dokumen tidak mengubah kode,
kontrak API, skema database, atau perilaku AI/MCP saat ini.

## 1. Prinsip Arsitektur

Vero memakai pembagian kewenangan berikut:

| Komponen | Kewenangan |
|---|---|
| **LLM** | Reasoning, memahami bahasa natural, menyusun rencana, memilih tool dari tool set yang diizinkan |
| **Tools** | Capability terbatas: pencarian, quote, availability, draft, booking command, payment command |
| **Backend** | Authority: validasi input, harga, availability policy, ownership, state transition, commit transaksi |
| **Database** | State durable: conversation, plan, booking, payment, idempotency, audit, outbox |
| **Policy Engine** | Permission: siapa boleh melakukan apa, pada state apa, dengan data/scope apa |
| **Events** | Workflow communication: perubahan state, retry, notification, observability; bukan source of truth |

LLM tidak boleh menjadi sumber kebenaran harga, availability, booking status,
payment status, permission, atau memory durable. Tidak ada swarm atau autonomous
agent tanpa kebutuhan domain yang jelas.

## 2. Current Architecture

Arsitektur saat ini adalah monorepo tiga aplikasi:

```text
Customer Next.js / Backoffice Next.js
        -> HTTP proxy /api
Gin routes + middleware
        -> handlers
        -> domain services
        -> repository interfaces
        -> GORM/PostgreSQL

AIService -> OpenAI-compatible AI client
MCPService -> catalog + tool execution
Services  -> in-memory event bus -> staff SSE
```

Komponen penting:

- `backend/cmd/server/main.go`: wiring, `AutoMigrate`, HTTP server, cleanup,
  graceful shutdown.
- `backend/internal/services/ai_service.go`: `Chat`, `ChatStream`, tool loop,
  recommendation guard, booking-claim guard, memory summary.
- `backend/internal/services/mcp_service.go`: dispatch dan validasi dasar tool.
- `backend/internal/mcp/tools.go`: katalog tool dan `Enabled` flag.
- `backend/internal/services/booking_service.go`: pricing server-side,
  booking, transition status.
- `backend/internal/services/payment_service.go`: payment intent, webhook,
  signature, idempotency, booking confirmation.
- `backend/internal/events/bus.go`: pub/sub in-memory untuk SSE.
- `backend/internal/models/models.go`: state durable saat ini: `User`,
  `ChatSession`, `ChatMessage`, `Trip`, `Booking`, `Payment`, `AILog`,
  `ToolCall`.

### Batas current architecture

1. Intent, planning, tool selection, dan response policy masih terpusat dalam
   `AIService` + system prompt.
2. Tidak ada durable travel plan sebagai entity terpisah dari chat session.
3. Tidak ada policy engine eksplisit; guard tersebar di service dan tool.
4. Payment flow disimpan namun sengaja disabled oleh `PAYMENTS_ENABLED=false`.
5. Event bus in-memory tidak durable dan tidak multi-instance.
6. Memory summary berupa tail/truncation, bukan memory pipeline berlapis.
7. Audit tool memakai bounded `AuditPool`, tetapi belum menjadi workflow event
   yang durable dengan outbox.

Current design sudah benar untuk MVP: authority tetap berada di backend dan
tool execution tidak diserahkan ke provider AI.

## 3. Target Architecture

Target memakai satu **Agent Runtime** terkontrol, bukan banyak autonomous agent.
Runtime memanggil modul deterministik dan LLM hanya pada boundary reasoning.

```text
                    +----------------------+
Customer/Backoffice -> Conversation API    |
                    +----------+-----------+
                               |
                    +----------v-----------+
                    | Agent Runtime         |
                    | intent -> plan -> loop|
                    +----+-----+------+-----+
                         |     |      |
                  +------v+ +--v---+ +-v----------+
                  | Intent | |Plan | |Policy       |
                  | LLM    | |LLM  | |deterministic|
                  +--------+ +------+ +------------+
                         |            |
                    +----v------------v----+
                    | Tool Gateway          |
                    | schema/auth/idempotent|
                    +----+------------------+
                         |
        +----------------+--------------------------+
        | deterministic domain services              |
        | Trip | Availability | Booking | Payment   |
        | Support | Notification | Memory            |
        +----------------+--------------------------+
                         |
                 PostgreSQL + Outbox
                         |
                    Event consumers
```

### 3.1 Request lifecycle

```text
request
  -> authenticate/identify subject
  -> load session + active plan + relevant memory
  -> classify intent
  -> validate intent against policy and state
  -> create/update travel plan
  -> LLM proposes next action or asks clarification
  -> backend validates proposed tool call
  -> tool gateway executes deterministic capability
  -> persist result + state transition + outbox event atomically
  -> LLM narrates only committed facts
  -> response / stream
```

LLM output is always a **proposal**. Tool Gateway and domain services decide
whether proposal is executable.

## 4. Component Responsibilities

### 4.1 Intent understanding

Input: user message, session context, active plan, user role, channel.

LLM may extract:

- intent: discover, compare, plan, quote, check availability, select package,
  book, pay, cancel, modify, support, status, or general travel question;
- entities: destination, dates, pax, budget, preferences, booking/payment ID;
- missing slots and confidence;
- ambiguity requiring clarification.

Backend must:

- validate types, ranges, IDs, ownership, dates, and PII format;
- reject unsupported or contradictory intent;
- avoid executing side effects from classification alone;
- retain original message and normalized intent for audit.

Intent classification may use rules first for high-risk intents (`pay`,
`cancel`, `book`, `refund`, `change contact`). LLM handles natural language
variants, not authorization.

### 4.2 Travel planning

`TravelPlan` is a durable, versioned domain object separate from raw chat.
It contains goals, constraints, selected trip, dates, pax, preferences,
quotes, assumptions, and plan status.

LLM may:

- propose itinerary structure and alternatives;
- identify missing information;
- explain trade-offs;
- summarize a plan for the user.

Backend owns:

- normalized plan fields;
- package IDs and published catalog references;
- price and availability facts;
- plan version and optimistic concurrency;
- conversion from proposal to booking command.

The plan is advisory until user confirmation and backend validation.

### 4.3 Tool planning

LLM chooses the next capability from a **scoped tool manifest**. It must not
invent tools, arguments, permissions, or state transitions.

Tool planner output:

```json
{
  "action": "get_trip_detail",
  "arguments": {"trip_id": "..."},
  "reason": "user requested package detail",
  "requires_confirmation": false
}
```

Tool planner does not directly call external systems. Gateway validates the
action, schema, policy, state, and idempotency key first.

### 4.4 Tool execution

Tool Gateway is the single entry point for AI-invoked capabilities. It handles:

- tool allow-list and version;
- JSON schema validation;
- subject/session/plan scope;
- timeout and retry class;
- idempotency key;
- redaction and audit;
- mapping internal errors to safe AI-facing errors;
- dispatch to deterministic domain service.

Tool implementations must not contain hidden LLM calls, hidden side effects,
or authority bypasses.

### 4.5 Policy and permission validation

Policy Engine evaluates:

```text
subject + role + channel + intent + tool + arguments + resource + state
```

Policy result:

```text
ALLOW
DENY(reason_code)
REQUIRE_CONFIRMATION(step)
REQUIRE_AUTHENTICATION
REQUIRE_HUMAN_REVIEW
```

Policy stays deterministic and testable. LLM may explain a denial but cannot
override it.

### 4.6 State management

Database stores authoritative state. Redis or an in-memory cache may hold
ephemeral locks/rate limits, never booking/payment truth.

Required durable records over time:

- `Conversation` / `ChatSession` and messages;
- `TravelPlan` and plan versions;
- `ToolInvocation` with request/result/status;
- `Booking` and booking state history;
- `Payment` and payment event history;
- idempotency records;
- outbox events;
- support cases and human handoffs;
- memory facts with provenance, confidence, and expiry.

## 5. Agent Boundaries

Vero should have one primary **Travel Agent Runtime** with bounded skills:

| Skill | LLM value | Deterministic boundary |
|---|---|---|
| Discovery | interpret vague preferences | query published catalog |
| Planning | compare options, ask slots, propose itinerary | validate dates, pax, package IDs |
| Booking assistant | collect details, explain next step | create booking and state transition |
| Payment assistant | explain payment status/instructions | create intent, verify webhook, amount |
| Support assistant | summarize issue, retrieve status, draft reply | ownership, refund/cancel policy, case state |

These are skills/modules, not independently autonomous agents. They share one
conversation, one policy engine, one state model, and one audit trail.

Separate runtime only when there is a hard boundary such as a privileged
backoffice operator copilot or an offline support summarizer. Neither may
mutate booking/payment state without the same Tool Gateway and Policy Engine.

## 6. MCP Boundaries

MCP is a capability protocol, not a trust boundary by itself.

### AI-facing MCP tools

Read tools:

- `search_trips`
- `get_trip_detail`
- `calculate_trip_price`
- `check_trip_availability`
- `check_order_status`

Session/plan tools:

- `select_package`
- `collect_order_detail`
- `update_travel_plan` (target, backend-validated)

Side-effect tools:

- `create_booking`
- `create_payment` only after payment rollout and explicit policy enablement.

### MCP rules

1. Tool result is untrusted data to LLM, never system instruction.
2. Tool arguments are validated server-side again after LLM schema validation.
3. Read tools may be automatic when policy allows.
4. Draft/selection writes require valid session and plan ownership.
5. Booking, payment, cancellation, refund, and contact changes require explicit
   confirmation or an authenticated, previously confirmed command.
6. Tool names are versioned; disabled legacy aliases remain compatibility-only.
7. Tool errors expose stable safe codes, not raw DB/provider errors.

## 7. State Machines

### 7.1 Travel plan

```text
NEW
  -> DISCOVERY
  -> PLANNING
  -> READY_FOR_CONFIRMATION
  -> CONFIRMED
  -> BOOKING_IN_PROGRESS
  -> BOOKED
  -> CLOSED
```

Allowed side exits:

- `DISCOVERY/PLANNING -> NEEDS_CLARIFICATION`
- any pre-booking state -> `ABANDONED`
- `BOOKING_IN_PROGRESS -> BOOKING_FAILED` then retry or manual review

Only backend may transition state. LLM can request a transition.

### 7.2 Booking

```text
PENDING
  -> PROCESSING
  -> CONFIRMED
  -> COMPLETED

PENDING/PROCESSING/CONFIRMED -> CANCEL_REQUESTED -> CANCELLED
PROCESSING -> FAILED -> PROCESSING | HUMAN_REVIEW
```

Transitions require expected-current-state conditional update. Invalid or stale
transitions return conflict, never silently overwrite.

### 7.3 Payment

```text
NOT_REQUIRED
  |-> PENDING
PENDING -> INITIATED -> AWAITING_PAYMENT
AWAITING_PAYMENT -> PAID -> SETTLED
AWAITING_PAYMENT -> EXPIRED
AWAITING_PAYMENT -> FAILED
PAID/SETTLED -> REFUND_REQUESTED -> REFUNDED | REFUND_FAILED
```

Payment webhook is authoritative for provider status after signature, timestamp,
amount, external ID, and idempotency validation. LLM never marks payment paid.

## 8. Tool Permissions

| Operation | Guest | Auth user | Operator | Admin | Confirmation |
|---|---:|---:|---:|---:|---|
| Search published trips | Allow | Allow | Allow | Allow | No |
| View trip detail/quote | Allow | Allow | Allow | Allow | No |
| Check availability | Allow | Allow | Allow | Allow | No |
| Save selection/draft | Cookie-owned session | Own plan | Scoped | Scoped | No |
| Create booking | Allow if guest flow policy permits | Own plan | Manual action | Manual action | Yes |
| Create payment | Disabled until rollout | Own booking | Scoped | Scoped | Yes |
| Cancel booking | Deny or support handoff | Own booking + policy | Scoped | Scoped | Yes |
| Refund | Deny | Support request only | Human review | Authorized policy | Yes |
| Change user/role | Deny | Deny | Deny | Admin API only | Yes |

Guest booking remains a product decision. If enabled, guest ownership is the
HttpOnly session cookie plus a server-issued confirmation token; never a client
supplied session ID.

## 9. Booking Workflow

1. User expresses intent; Intent Understanding extracts missing slots.
2. Agent asks for missing pax, date, contact, and selected package.
3. `search_trips`, `get_trip_detail`, `calculate_trip_price`, and
   `check_trip_availability` return backend facts.
4. Backend stores a `TravelPlan` draft and quote snapshot with expiry.
5. Agent presents summary: package, date, pax, contact, price, assumptions.
6. User explicitly confirms booking.
7. Policy Engine checks subject, ownership, package publication, quote expiry,
   availability, pax limits, and duplicate booking.
8. Backend creates booking with server-side price in one transaction and writes
   idempotency record/outbox event.
9. Agent reports only committed booking ID/status.
10. Payment remains a separate workflow; booking success never implies payment
    success.

## 10. Payment Workflow

1. User asks to pay or backend determines payment is required.
2. Policy checks authenticated/owned booking, allowed method, amount, and state.
3. Backend creates payment intent with server-side booking amount and expiry.
4. Provider instructions are returned; LLM may explain them, not alter them.
5. Provider webhook enters dedicated endpoint.
6. Backend verifies signature over the exact raw body plus required provider
   fields, timestamp tolerance, external ID, and amount.
7. Conditional idempotent update changes payment state.
8. Booking confirmation happens only after valid paid/settled transition.
9. Outbox emits `payment.updated`, `booking.confirmed`, and notification jobs.
10. Unknown, duplicate, expired, or contradictory events are audited and do not
    downgrade settled state.

`create_payment` remains disabled until DOKU enablement, webhook verification,
reconciliation, frontend UX, and operational runbook are complete.

## 11. Post-booking Support

Support intent routes to read-only tools first:

- check booking/payment status;
- retrieve itinerary and confirmed facts;
- explain next steps;
- create support case;
- request cancellation/refund/change.

Mutations use policy and human review. Support case state:

```text
OPEN -> TRIAGED -> WAITING_CUSTOMER | WAITING_PROVIDER | HUMAN_REVIEW
     -> RESOLVED -> REOPENED
```

LLM can summarize timeline and draft customer-facing text. It cannot promise a
refund, override provider state, expose another user's PII, or close a case
without backend state transition.

## 12. Failure Handling and Retry Strategy

Classify failures before retry:

| Class | Example | Action |
|---|---|---|
| Validation | bad UUID, pax out of range | No retry; safe user explanation |
| Policy | unauthorized, wrong state | No retry; deny or handoff |
| Conflict | stale plan/version, duplicate | Reload state; ask confirmation |
| Transient provider | timeout, 502, connection reset | Bounded exponential retry with jitter |
| Rate limit | 429/provider quota | Retry-after or defer job |
| Permanent provider | invalid credentials/schema | Disable path, alert, human review |
| Unknown | unexpected error | No blind retry; audit + correlation ID |

Rules:

- Read-only AI/provider calls: at most 2-3 retries, only on transient errors.
- Booking/payment writes: retry only with same idempotency key and bounded
  worker/job; never replay as a new command.
- Webhooks: return provider-safe acknowledgement after durable receipt; process
  asynchronously when provider contract allows.
- LLM tool loop: cap rounds, token budget, wall-clock budget, and duplicate
  calls. A failed tool must not be hidden by a confident final answer.
- Backoff: exponential with jitter; no synchronized fixed retry loops.

## 13. Idempotency Strategy

Every side-effect command carries or derives:

```text
idempotency_key = hash(subject, session, plan_version, intent, client_key)
```

Preferred client header: `Idempotency-Key`. Backend stores key, operation,
subject, request hash, status, resource ID, response, and expiry.

Rules:

1. Same key + same request returns original result.
2. Same key + different request returns conflict.
3. Booking key is unique per confirmed plan version.
4. Payment key is unique per booking + payment attempt.
5. Provider webhook deduplicates by provider event ID, or stable external ID +
   status when event ID is unavailable.
6. DB unique constraints and conditional updates remain final protection; cache
   locks alone are insufficient.

## 14. Memory Strategy

Use four memory layers:

1. **Working memory** — current request, active tool results, current plan;
   short-lived and bounded.
2. **Conversation memory** — recent messages, stored in current chat tables;
   exact transcript with retention policy and PII controls.
3. **Episodic memory** — durable events such as selected package, quote,
   booking, support case; sourced from DB records, not generated summaries.
4. **Semantic preference memory** — explicit user preferences (budget,
   destinations, mobility, dietary needs) with source, confidence, consent,
   last-used time, and expiry.

Memory retrieval is deterministic: scope by subject/session, rank by relevance
and recency, cap tokens, redact secrets/PII not needed for task. LLM-generated
summary is a convenience projection and never overwrites source facts.

Retention:

- guest transcript and cookie session: current configured TTL;
- booking/payment/support facts: business/legal retention;
- preference memory: user deletion/export and consent controls;
- audit logs: immutable or append-only retention with restricted access.

## 15. Observability Strategy

Every request, agent run, tool call, booking command, payment attempt, and event
has a correlation chain:

```text
request_id -> conversation_id -> plan_id/version -> agent_run_id
           -> tool_call_id -> booking_id/payment_id -> event_id
```

Metrics:

- intent classification latency/confidence/fallback rate;
- LLM TTFT, total latency, tokens, cost, provider/model/error;
- tool success/failure/latency/retry and policy denial counts;
- plan-to-book conversion and clarification turns;
- booking conflicts, duplicate prevention, human handoffs;
- payment initiation, webhook lag, mismatch, duplicate, reconciliation;
- queue depth, outbox lag, event delivery, DB pool saturation;
- memory retrieval hit rate and token contribution.

Logs are structured, redacted, and never include raw secrets, refresh tokens,
full payment payloads, or unnecessary contact PII. Traces cover HTTP, LLM,
repository calls, tool gateway, and async workers. Persisted audit is separate
from user-visible conversation.

## 16. Security Model

- Authentication remains access JWT + revocable HttpOnly refresh session.
- Guest ownership remains cookie-bound and server-validated.
- RBAC and resource ownership are enforced before every mutation.
- Policy Engine is fail-closed for unknown tool, state, role, or resource.
- Tool results are treated as untrusted catalog/provider data; prompt injection
  cannot change system policy.
- LLM receives least-privilege, scoped tool manifest and redacted context.
- Server computes prices, validates availability, and owns state transitions.
- Payment webhooks verify exact raw body, signature, timestamp, amount, external
  ID, and idempotency before state mutation.
- Secrets stay in environment/secret manager; no secret enters prompt, logs,
  memory, or tool result.
- Uploads, rate limits, body limits, request context propagation, and SSE guards
  remain active.
- Admin and operator tools have separate scopes; admin privilege is never an
  LLM-granted attribute.

## 17. Migration Plan

Migration is incremental and backward-compatible.

### Phase 0 — Contract and measurement

- Freeze current API/tool contracts and disabled payment behavior.
- Add architecture tests around existing state transitions, ownership, pricing,
  booking-claim guard, and payment guards.
- Define correlation IDs, tool error codes, and event naming.

### Phase 1 — Extract boundaries without behavior change

- Introduce internal `Intent`, `TravelPlanDraft`, `ToolCommand`, `PolicyDecision`,
  and `AgentRun` types.
- Move existing `AIService` tool loop behind `AgentRuntime` facade.
- Move MCP dispatch behind `ToolGateway`; keep existing tool names and result
  shapes as adapters.
- Keep `BookingService`, `PaymentService`, repositories, and routes authoritative.

### Phase 2 — Explicit policy and durable plan

- Add policy interfaces and deterministic policy tests.
- Add `TravelPlan` plus versioning, quote snapshot, confirmation, and expiry.
- Link booking to confirmed plan version without removing legacy session marker
  until migration is complete.
- Add idempotency table/constraints for booking and payment commands.

### Phase 3 — Durable workflow communication

- Add transactional outbox alongside state writes.
- Replace critical reliance on in-memory event bus with outbox consumers.
- Keep SSE as a projection for operator UX; reconnect/replay from durable events.
- Introduce bounded workers for provider notifications, reconciliation, and
  support tasks.

### Phase 4 — Payment enablement gate

Enable `create_payment` only after:

- DOKU sandbox contract tests;
- exact raw-body signature tests;
- amount and currency validation;
- duplicate/replay/concurrent webhook tests;
- payment reconciliation and manual review path;
- frontend payment status UX;
- alerts and runbook;
- explicit production configuration review.

### Phase 5 — Memory and support

- Separate source facts from generated summaries.
- Add consented preference memory and deletion/export.
- Add support case workflow and human handoff.
- Add retrieval evaluation: correctness, leakage, stale preference rate.

### Phase 6 — Scale only after evidence

- Move rate limiting/event delivery to shared infrastructure for multi-instance.
- Add queue/broker only for durable asynchronous workflows.
- Add model routing/fallback based on measured latency, cost, and quality.
- Do not introduce autonomous sub-agents unless a benchmark proves a bounded
  domain needs it and policy/audit boundaries remain identical.

## 18. Internal Consistency Rules

The proposal is internally consistent if these invariants hold:

1. LLM can propose; only backend can commit.
2. Tool Gateway is the only path from LLM output to side effects.
3. Policy runs before every side effect, including retries.
4. Database state beats chat text, LLM claims, cache, and events.
5. Events communicate committed changes; they do not authorize changes.
6. Payment status comes from validated provider events or authorized backend
   actions, never from LLM narration.
7. Every write is idempotent or explicitly non-retryable.
8. Every failure has a safe user message, server diagnostic, and correlation ID.
9. One Travel Agent Runtime owns the conversation; skills do not become hidden
   autonomous agents.
10. Existing `create_payment` disabled state remains unchanged until the gate in
    Phase 4 passes.

## 19. Decision Summary

Keep deterministic:

- authentication, authorization, policy, ownership;
- catalog query, pricing, availability, date/pax validation;
- plan persistence/versioning;
- booking/payment/support state machines;
- transactions, idempotency, retries, webhooks, reconciliation;
- memory scope, retention, redaction, and source facts;
- events, audit, metrics, and security controls.

Use LLM for:

- natural-language intent/entity extraction;
- clarification and preference interpretation;
- travel-plan proposals and trade-off explanations;
- selecting among already-authorized tools;
- summarizing committed facts for the user;
- support classification and draft responses.

This creates a production-grade travel agent without turning Vero into an
unbounded autonomous system.
