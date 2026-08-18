# Graph Report - BitFecta_VeroAiAgents  (2026-08-18)

## Corpus Check
- 164 files · ~99,057 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 1160 nodes · 2458 edges · 91 communities (65 shown, 26 thin omitted)
- Extraction: 90% EXTRACTED · 10% INFERRED · 0% AMBIGUOUS · INFERRED: 236 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `9fa12d33`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Success
- backoffice-frontend/src/lib/api.ts
- New
- main
- dto.go
- ToolResult
- MCPService
- compilerOptions
- ChatInterface.tsx
- Services
- orders-panel.tsx
- ai_client.go
- mockMCPRepo
- use-trip-form.ts
- devDependencies
- form-section.tsx
- Context
- Backend - Service Layer, Business Logic, dan Integrasi
- basic-info-section.tsx
- Vero TravelOS — Backoffice Frontend
- Context
- use-package-references.ts
- ChatSession
- Booking
- dependencies
- trip-form-screen.tsx
- Trip
- trip-card.tsx
- @types/react
- dependencies
- TripsListScreen
- trips-list-screen.tsx
- AuditPool
- DB
- Repository
- setup-postgres.sh
- backoffice-frontend/package.json
- pricing-section.tsx
- frontend/package.json
- frontend/src/app/layout.tsx
- okResponse
- media-section.tsx
- PaymentService
- extends
- DOKUClient
- T
- cn
- route.ts
- .EventStream
- Payment
- models.AILog
- backoffice-frontend/next.config.mjs
- backoffice-frontend/postcss.config.mjs
- backoffice-frontend/tailwind.config.ts
- frontend/next.config.mjs
- frontend/postcss.config.mjs
- frontend/tailwind.config.ts
- github.com/rozzi/vero-ai-travel-agents/backend
- Context
- User
- BookingService
- trip-card-context-menu.tsx
- HandlerFunc
- Context
- UUID
- Context
- T
- ToolCall
- Limit
- Limiter
- Map
- Mutex
- Once
- WaitGroup

## God Nodes (most connected - your core abstractions)
1. `Success()` - 35 edges
2. `Trip` - 29 edges
3. `New()` - 28 edges
4. `mockMCPRepo` - 24 edges
5. `ToolResult` - 23 edges
6. `apiFetch()` - 21 edges
7. `Booking` - 21 edges
8. `ChatSession` - 21 edges
9. `AIService` - 21 edges
10. `MCPService` - 21 edges

## Surprising Connections (you probably didn't know these)
- `buildTripFromRequest()` --calls--> `normalize()`  [INFERRED]
  backend/internal/services/trip_service.go → backend/internal/services/helpers.go
- `include` --extends--> `next-env.d.ts`  [EXTRACTED]
  backoffice-frontend/tsconfig.json → frontend/tsconfig.json
- `include` --extends--> `.next/types/**/*.ts`  [EXTRACTED]
  backoffice-frontend/tsconfig.json → frontend/tsconfig.json
- `New()` --calls--> `NewAuditPool()`  [INFERRED]
  backend/internal/services/services.go → backend/internal/services/audit_pool.go
- `TestMCPService_AuditFallbackSync()` --calls--> `clonePayload()`  [INFERRED]
  backend/internal/services/audit_pool_test.go → backend/internal/services/mcp_service.go

## Import Cycles
- None detected.

## Communities (91 total, 26 thin omitted)

### Community 0 - "Success"
Cohesion: 0.06
Nodes (55): ClearGuestSessionCookie(), ClearRefreshCookie(), GetGuestSessionCookie(), GetRefreshCookie(), Context, parseSameSite(), SetGuestSessionCookie(), SetRefreshCookie() (+47 more)

### Community 1 - "backoffice-frontend/src/lib/api.ts"
Cohesion: 0.07
Nodes (58): geistMono, geistSans, metadata, AppShell(), AuthState, isPublicRoute(), publicRoutes, cacheTrips() (+50 more)

### Community 2 - "New"
Cohesion: 0.13
Nodes (31): Time, NewBus(), DB, Repository, New(), discountedTrip(), getf(), T (+23 more)

### Community 3 - "main"
Cohesion: 0.07
Nodes (37): Claims, auth.JWTService, TokenPair, main(), startChatSessionCleanup(), Duration, UUID, IsAudience() (+29 more)

### Community 4 - "dto.go"
Cohesion: 0.06
Nodes (40): B, LogSecurity(), UUID, Context, UUID, firstNonEmpty(), slugify(), BenchmarkSlugify() (+32 more)

### Community 5 - "ToolResult"
Cohesion: 0.10
Nodes (33): Message, ToolCall, Context, Handler, extractRecommendedPackages(), failedSearchTripsAlreadySelected(), formatAILogTrackingCode(), ChatMessage (+25 more)

### Community 6 - "MCPService"
Cohesion: 0.13
Nodes (25): priceBreakdown(), tripAdultPrice(), tripChildPrice(), firstNonZero(), Time, limitSlice(), limitString(), normalize() (+17 more)

### Community 7 - "compilerOptions"
Cohesion: 0.04
Nodes (46): compilerOptions, allowJs, esModuleInterop, incremental, isolatedModules, jsx, lib, module (+38 more)

### Community 8 - "ChatInterface.tsx"
Cohesion: 0.09
Nodes (29): TripDetailPage(), RecommendationCard(), RecommendationCardProps, AssistantMessage, AssistantMessageProps, ChatInterface(), ChatMessage, nextMessageId() (+21 more)

### Community 9 - "Services"
Cohesion: 0.08
Nodes (18): Connect(), Context, DB, Duration, Handler, Time, New(), Context (+10 more)

### Community 10 - "orders-panel.tsx"
Cohesion: 0.12
Nodes (27): customerEmail(), customerName(), destination(), DetailDrawer(), filters, formatDate(), formatDateTime(), formatRelative() (+19 more)

### Community 11 - "ai_client.go"
Cohesion: 0.10
Nodes (36): Client, CompletionRequest, CompletionResponse, FunctionCall, FunctionSpec, ResponseFormat, ToolCall, ToolDef (+28 more)

### Community 12 - "mockMCPRepo"
Cohesion: 0.12
Nodes (11): Context, Repository, ToolCall, ChatMessage, Context, Time, ToolCall, UUID (+3 more)

### Community 13 - "use-trip-form.ts"
Cohesion: 0.20
Nodes (18): computeDiscountPercent(), formatDateForInput(), mapTripToForm(), normalizeAmenities(), normalizeCategory(), normalizeItineraries(), normalizeScheduleType(), parseDuration() (+10 more)

### Community 14 - "devDependencies"
Cohesion: 0.10
Nodes (24): devDependencies, eslint, eslint-config-next, postcss, tailwindcss, @types/node, @types/react-dom, typescript (+16 more)

### Community 15 - "form-section.tsx"
Cohesion: 0.18
Nodes (11): AmenitiesSection(), Props, HighlightsSection(), Props, ItinerarySection(), Props, Props, ReferenceSection() (+3 more)

### Community 16 - "Context"
Cohesion: 0.26
Nodes (5): ChatMessage, Context, Repository, Time, UUID

### Community 17 - "Backend - Service Layer, Business Logic, dan Integrasi"
Cohesion: 0.12
Nodes (16): AIService - Inti Produk, AnalyticsService, AuthService, Backend - Service Layer, Business Logic, dan Integrasi, Background Jobs, Queue, Cache, Scheduler, BookingService & PaymentService, Dependency Inversion (SEC-27, 1 Agu 2026), Integrasi Eksternal (+8 more)

### Community 18 - "basic-info-section.tsx"
Cohesion: 0.20
Nodes (10): BasicInfoSection(), Props, Props, SchedulingSection(), DateRange(), DurationPicker(), Field(), Label() (+2 more)

### Community 19 - "Vero TravelOS — Backoffice Frontend"
Cohesion: 0.25
Nodes (7): Auth & Sesi, Fitur Aktif, Form Paket, Konfigurasi & Proxy API, Menjalankan, Vero TravelOS — Backoffice Frontend, Yang Belum Aktif (placeholder / mock)

### Community 20 - "Context"
Cohesion: 0.26
Nodes (4): Context, Repository, Time, UUID

### Community 21 - "use-package-references.ts"
Cohesion: 0.38
Nodes (6): fetchPackages(), isTripId(), PackageReference, SearchState, usePackageReferences(), TripPackage

### Community 22 - "ChatSession"
Cohesion: 0.22
Nodes (14): ChatMessage, DB, Time, UUID, IsPaymentSuccess(), NormalizePaymentStatus(), DeletedAt, AuthSession (+6 more)

### Community 23 - "Booking"
Cohesion: 0.36
Nodes (4): Context, Repository, UUID, Booking

### Community 24 - "dependencies"
Cohesion: 0.15
Nodes (13): dependencies, clsx, lucide-react, next, react, react-dom, clsx, next (+5 more)

### Community 25 - "trip-form-screen.tsx"
Cohesion: 0.20
Nodes (7): Props, SummarySection(), TripFormFooter(), TripFormFooterProps, TripFormScreen(), applyControlledState(), useTripForm()

### Community 26 - "Trip"
Cohesion: 0.35
Nodes (5): Context, Repository, UUID, Trip, TripRepositoryFilter

### Community 27 - "trip-card.tsx"
Cohesion: 0.32
Nodes (7): TripCard(), TripCardProps, formatDateRange(), formatTripPax(), getStatusTone(), formatIDR(), getDiscountMeta()

### Community 28 - "@types/react"
Cohesion: 0.67
Nodes (3): @types/react, @types/react, @types/react

### Community 29 - "dependencies"
Cohesion: 0.20
Nodes (10): tailwind-merge, dependencies, clsx, next, react, tailwind-merge, clsx, next (+2 more)

### Community 31 - "trips-list-screen.tsx"
Cohesion: 0.20
Nodes (8): ToastNotification(), ToastNotificationProps, ToastState, CreateTripCard(), EmptyPackagesState(), OnDevelopmentPanel(), TripsSearchHeader(), TripsSearchHeaderProps

### Community 32 - "AuditPool"
Cohesion: 0.09
Nodes (23): TestIPRateLimiterCapUsesConstantTimeCounter(), TestIPRateLimiterEvictsByLastUseWithoutConsumingToken(), NewAuditPool(), TestAuditPool_StopIsIdempotent(), TestAuditPool_SubmitAndDrain(), TestAuditPool_SubmitNonBlockingWhenFull(), TestAuditPoolSubmitConcurrentWithStop(), TestMCPService_AuditFallbackSync() (+15 more)

### Community 35 - "setup-postgres.sh"
Cohesion: 0.56
Nodes (8): docker_ready(), log(), print_docker_permission_help(), setup_native(), setup_with_docker(), setup-postgres.sh script, wait_for_postgres(), warn()

### Community 36 - "backoffice-frontend/package.json"
Cohesion: 0.22
Nodes (8): name, private, scripts, build, dev, lint, start, version

### Community 37 - "pricing-section.tsx"
Cohesion: 0.31
Nodes (6): TripFormStaticDefaults, computePercentFromPrice(), computePriceFromPercent(), PricingSection(), Props, Checkbox()

### Community 38 - "frontend/package.json"
Cohesion: 0.22
Nodes (8): name, private, scripts, build, dev, lint, start, version

### Community 39 - "frontend/src/app/layout.tsx"
Cohesion: 0.31
Nodes (5): inter, metadata, NavItem(), Sidebar(), cn()

### Community 40 - "okResponse"
Cohesion: 0.39
Nodes (5): Context, Handler, okResponse(), op(), H

### Community 41 - "media-section.tsx"
Cohesion: 0.36
Nodes (5): MediaSection(), Props, UploadBox(), UploadPreview(), assetURL()

### Community 42 - "PaymentService"
Cohesion: 0.27
Nodes (5): Context, UUID, PaymentRepository, PaymentRepository, PaymentService

### Community 43 - "extends"
Cohesion: 0.40
Nodes (4): extends, extends, next/core-web-vitals, next/typescript

### Community 46 - "cn"
Cohesion: 0.15
Nodes (16): ConfirmModal(), ConfirmModalProps, CopyableField(), StatsCard(), InfoModal(), TripsToolbar(), TripsToolbarProps, ActivePanel (+8 more)

### Community 49 - "Payment"
Cohesion: 0.44
Nodes (4): Context, Repository, UUID, Payment

### Community 50 - "models.AILog"
Cohesion: 0.33
Nodes (6): context.Context, sync.Mutex, models.AILog, models.ToolCall, mockAuditWriter, stalledWriter

### Community 75 - "Context"
Cohesion: 0.36
Nodes (3): PaymentSuccessStatuses(), Context, Repository

### Community 76 - "User"
Cohesion: 0.46
Nodes (4): Context, Repository, UUID, User

### Community 77 - "BookingService"
Cohesion: 0.46
Nodes (4): Context, UUID, BookingRepository, BookingService

### Community 78 - "trip-card-context-menu.tsx"
Cohesion: 0.38
Nodes (6): getActionClasses(), getMenuActions(), MenuAction, MenuActionTone, TripCardContextMenu(), TripCardContextMenuProps

## Knowledge Gaps
- **146 isolated node(s):** `Lokasi Kode Inti`, `AuthService`, `AIService - Inti Produk`, `MCPService`, `TripService` (+141 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **26 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Config` connect `Success` to `New`, `main`, `dto.go`, `ToolResult`, `Services`, `PaymentService`?**
  _High betweenness centrality (0.047) - this node is a cross-community bridge._
- **Why does `New()` connect `New` to `AuditPool`, `Success`, `main`, `dto.go`, `ToolResult`, `Services`, `PaymentService`, `ai_client.go`, `mockMCPRepo`, `BookingService`?**
  _High betweenness centrality (0.043) - this node is a cross-community bridge._
- **Why does `Services` connect `Services` to `Success`, `AuditPool`, `New`, `main`, `dto.go`, `ToolResult`, `MCPService`, `PaymentService`, `BookingService`?**
  _High betweenness centrality (0.038) - this node is a cross-community bridge._
- **Are the 33 inferred relationships involving `Success()` (e.g. with `respondAuthIssue()` and `TestAPIResponses()`) actually correct?**
  _`Success()` has 33 INFERRED edges - model-reasoned connections that need verification._
- **Are the 20 inferred relationships involving `New()` (e.g. with `TestCalculateTripPriceAdultChildCombination()` and `TestCalculateTripPriceMatchesBooking()`) actually correct?**
  _`New()` has 20 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Lokasi Kode Inti`, `AuthService`, `AIService - Inti Produk` to the rest of the system?**
  _146 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Success` be split into smaller, more focused modules?**
  _Cohesion score 0.05891016200294551 - nodes in this community are weakly interconnected._