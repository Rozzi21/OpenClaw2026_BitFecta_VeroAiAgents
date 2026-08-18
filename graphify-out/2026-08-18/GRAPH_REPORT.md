# Graph Report - BitFecta_VeroAiAgents  (2026-08-17)

## Corpus Check
- 162 files · ~98,335 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 1120 nodes · 2418 edges · 73 communities (57 shown, 16 thin omitted)
- Extraction: 90% EXTRACTED · 10% INFERRED · 0% AMBIGUOUS · INFERRED: 236 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `d424265c`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Success
- backoffice-frontend/src/lib/api.ts
- New
- main
- Trip
- ChatSession
- ToolResult
- compilerOptions
- ChatInterface.tsx
- Services
- orders-panel.tsx
- ai_client.go
- mockMCPRepo
- use-trip-form.ts
- devDependencies
- trip-form-screen.tsx
- Context
- Database
- basic-info-section.tsx
- Vero TravelOS — Backoffice Frontend
- models.go
- use-package-references.ts
- toast-notification.tsx
- Booking
- dependencies
- trips/page.tsx
- OpenAITools
- trip-card.tsx
- @types/react
- dependencies
- TripsListScreen
- trips-list-screen.tsx
- Context
- DB
- Repository
- setup-postgres.sh
- backoffice-frontend/package.json
- pricing-section.tsx
- frontend/package.json
- frontend/src/app/layout.tsx
- okResponse
- media-section.tsx
- trip-card-context-menu.tsx
- extends
- DOKUClient
- T
- route.ts
- .EventStream
- trip-form-footer.tsx
- backoffice-frontend/next.config.mjs
- backoffice-frontend/postcss.config.mjs
- backoffice-frontend/tailwind.config.ts
- frontend/next.config.mjs
- frontend/postcss.config.mjs
- frontend/tailwind.config.ts
- github.com/rozzi/vero-ai-travel-agents/backend

## God Nodes (most connected - your core abstractions)
1. `Success()` - 34 edges
2. `New()` - 31 edges
3. `Trip` - 29 edges
4. `mockMCPRepo` - 24 edges
5. `ToolResult` - 24 edges
6. `apiFetch()` - 21 edges
7. `ChatSession` - 21 edges
8. `Booking` - 21 edges
9. `AIService` - 21 edges
10. `MCPService` - 21 edges

## Surprising Connections (you probably didn't know these)
- `buildTripFromRequest()` --calls--> `normalize()`  [INFERRED]
  backend/internal/services/trip_service.go → backend/internal/services/helpers.go
- `include` --extends--> `next-env.d.ts`  [EXTRACTED]
  backoffice-frontend/tsconfig.json → frontend/tsconfig.json
- `include` --extends--> `.next/types/**/*.ts`  [EXTRACTED]
  backoffice-frontend/tsconfig.json → frontend/tsconfig.json
- `Role()` --calls--> `Forbidden()`  [INFERRED]
  backend/internal/middlewares/middlewares.go → backend/internal/utils/response.go
- `Auth()` --calls--> `Unauthorized()`  [INFERRED]
  backend/internal/middlewares/middlewares.go → backend/internal/utils/response.go

## Import Cycles
- None detected.

## Communities (73 total, 16 thin omitted)

### Community 0 - "Success"
Cohesion: 0.06
Nodes (49): ClearGuestSessionCookie(), ClearRefreshCookie(), GetGuestSessionCookie(), GetRefreshCookie(), Context, parseSameSite(), SetGuestSessionCookie(), SetRefreshCookie() (+41 more)

### Community 1 - "backoffice-frontend/src/lib/api.ts"
Cohesion: 0.06
Nodes (62): geistMono, geistSans, metadata, AppShell(), AuthState, isPublicRoute(), publicRoutes, ActivePanel (+54 more)

### Community 2 - "New"
Cohesion: 0.07
Nodes (47): Time, NewBus(), DB, Repository, New(), Context, UUID, NewAuditPool() (+39 more)

### Community 3 - "main"
Cohesion: 0.07
Nodes (36): main(), startChatSessionCleanup(), getBoolEnv(), getEnv(), getFloat(), getInt(), Load(), parseCSVEnv() (+28 more)

### Community 4 - "Trip"
Cohesion: 0.08
Nodes (28): B, Context, Repository, UUID, firstNonEmpty(), slugify(), BenchmarkSlugify(), T (+20 more)

### Community 5 - "ChatSession"
Cohesion: 0.10
Nodes (32): Message, ToolCall, Context, Handler, ChatMessage, failedSearchTripsAlreadySelected(), formatAILogTrackingCode(), ChatMessage (+24 more)

### Community 6 - "ToolResult"
Cohesion: 0.13
Nodes (28): extractRecommendedPackages(), hasSearchTripsAlternative(), priceBreakdown(), tripAdultPrice(), tripChildPrice(), firstNonZero(), Time, limitSlice() (+20 more)

### Community 7 - "compilerOptions"
Cohesion: 0.04
Nodes (46): compilerOptions, allowJs, esModuleInterop, incremental, isolatedModules, jsx, lib, module (+38 more)

### Community 8 - "ChatInterface.tsx"
Cohesion: 0.09
Nodes (29): TripDetailPage(), RecommendationCard(), RecommendationCardProps, AssistantMessage, AssistantMessageProps, ChatInterface(), ChatMessage, nextMessageId() (+21 more)

### Community 9 - "Services"
Cohesion: 0.06
Nodes (35): Claims, JWTService, TokenPair, LogSecurity(), Duration, UUID, IsAudience(), Context (+27 more)

### Community 10 - "orders-panel.tsx"
Cohesion: 0.11
Nodes (29): CopyableField(), customerEmail(), customerName(), destination(), DetailDrawer(), filters, formatDate(), formatDateTime() (+21 more)

### Community 11 - "ai_client.go"
Cohesion: 0.14
Nodes (26): Client, CompletionRequest, CompletionResponse, FunctionCall, FunctionSpec, ResponseFormat, ToolCall, ToolDef (+18 more)

### Community 12 - "mockMCPRepo"
Cohesion: 0.13
Nodes (10): Context, Repository, ToolCall, ChatMessage, Context, Time, ToolCall, UUID (+2 more)

### Community 13 - "use-trip-form.ts"
Cohesion: 0.16
Nodes (22): computeDiscountPercent(), formatDateForInput(), mapTripToForm(), normalizeAmenities(), normalizeCategory(), normalizeItineraries(), normalizeScheduleType(), parseDuration() (+14 more)

### Community 14 - "devDependencies"
Cohesion: 0.10
Nodes (24): devDependencies, eslint, eslint-config-next, postcss, tailwindcss, @types/node, @types/react-dom, typescript (+16 more)

### Community 15 - "trip-form-screen.tsx"
Cohesion: 0.14
Nodes (16): AmenitiesSection(), Props, HighlightsSection(), Props, ItinerarySection(), Props, Props, ReferenceSection() (+8 more)

### Community 16 - "Context"
Cohesion: 0.26
Nodes (5): ChatMessage, Context, Repository, Time, UUID

### Community 17 - "Database"
Cohesion: 0.20
Nodes (8): Connect(), Context, DB, Duration, Handler, Time, New(), Database

### Community 18 - "basic-info-section.tsx"
Cohesion: 0.20
Nodes (10): BasicInfoSection(), Props, Props, SchedulingSection(), DateRange(), DurationPicker(), Field(), Label() (+2 more)

### Community 19 - "Vero TravelOS — Backoffice Frontend"
Cohesion: 0.25
Nodes (7): Auth & Sesi, Fitur Aktif, Form Paket, Konfigurasi & Proxy API, Menjalankan, Vero TravelOS — Backoffice Frontend, Yang Belum Aktif (placeholder / mock)

### Community 20 - "models.go"
Cohesion: 0.06
Nodes (32): DB, Time, UUID, IsPaymentSuccess(), NormalizePaymentStatus(), Context, Repository, Time (+24 more)

### Community 21 - "use-package-references.ts"
Cohesion: 0.47
Nodes (5): fetchPackages(), isTripId(), PackageReference, SearchState, usePackageReferences()

### Community 22 - "toast-notification.tsx"
Cohesion: 0.50
Nodes (3): ToastNotification(), ToastNotificationProps, ToastState

### Community 23 - "Booking"
Cohesion: 0.19
Nodes (10): UUID, Context, Repository, UUID, Context, UUID, BookingRepository, BookingRequest (+2 more)

### Community 24 - "dependencies"
Cohesion: 0.15
Nodes (13): dependencies, clsx, lucide-react, next, react, react-dom, clsx, next (+5 more)

### Community 26 - "OpenAITools"
Cohesion: 0.33
Nodes (10): ActiveCatalog(), Catalog(), OpenAITools(), requiredInputs(), T, TestOpenAITools_OnlyActiveToolsExposed(), TestOpenAITools_ParameterTypesNotForcedToString(), TestOpenAITools_RequiredArrays() (+2 more)

### Community 27 - "trip-card.tsx"
Cohesion: 0.28
Nodes (8): TripCard(), TripCardProps, ViewMode, formatDateRange(), formatTripPax(), getStatusTone(), formatIDR(), getDiscountMeta()

### Community 28 - "@types/react"
Cohesion: 0.67
Nodes (3): @types/react, @types/react, @types/react

### Community 29 - "dependencies"
Cohesion: 0.20
Nodes (10): tailwind-merge, dependencies, clsx, next, react, tailwind-merge, clsx, next (+2 more)

### Community 31 - "trips-list-screen.tsx"
Cohesion: 0.20
Nodes (10): ConfirmModal(), ConfirmModalProps, CreateTripCard(), EmptyPackagesState(), OnDevelopmentPanel(), TripsSearchHeader(), TripsSearchHeaderProps, TripsToolbar() (+2 more)

### Community 32 - "Context"
Cohesion: 0.36
Nodes (3): PaymentSuccessStatuses(), Context, Repository

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

### Community 42 - "trip-card-context-menu.tsx"
Cohesion: 0.38
Nodes (6): getActionClasses(), getMenuActions(), MenuAction, MenuActionTone, TripCardContextMenu(), TripCardContextMenuProps

### Community 43 - "extends"
Cohesion: 0.40
Nodes (4): extends, extends, next/core-web-vitals, next/typescript

## Knowledge Gaps
- **134 isolated node(s):** `Auth & Sesi`, `Form Paket`, `Yang Belum Aktif (placeholder / mock)`, `Konfigurasi & Proxy API`, `Menjalankan` (+129 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **16 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `New` to `Success`, `ChatSession`, `Services`, `ai_client.go`, `mockMCPRepo`, `models.go`, `Booking`?**
  _High betweenness centrality (0.056) - this node is a cross-community bridge._
- **Why does `Services` connect `Services` to `Success`, `New`, `main`, `Trip`, `ChatSession`, `ToolResult`, `Database`, `models.go`, `Booking`?**
  _High betweenness centrality (0.055) - this node is a cross-community bridge._
- **Why does `Config` connect `Success` to `New`, `main`, `ChatSession`, `Services`, `Database`, `models.go`?**
  _High betweenness centrality (0.047) - this node is a cross-community bridge._
- **Are the 32 inferred relationships involving `Success()` (e.g. with `respondAuthIssue()` and `.AdminCreateUser()`) actually correct?**
  _`Success()` has 32 INFERRED edges - model-reasoned connections that need verification._
- **Are the 23 inferred relationships involving `New()` (e.g. with `TestAuditPool_SubmitAndDrain()` and `TestAuditPool_SubmitNonBlockingWhenFull()`) actually correct?**
  _`New()` has 23 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Auth & Sesi`, `Form Paket`, `Yang Belum Aktif (placeholder / mock)` to the rest of the system?**
  _134 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Success` be split into smaller, more focused modules?**
  _Cohesion score 0.06373626373626373 - nodes in this community are weakly interconnected._