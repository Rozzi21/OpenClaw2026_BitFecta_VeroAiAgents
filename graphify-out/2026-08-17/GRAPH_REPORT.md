# Graph Report - BitFecta_VeroAiAgents  (2026-08-15)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 1115 nodes · 2416 edges · 75 communities (63 shown, 12 thin omitted)
- Extraction: 90% EXTRACTED · 10% INFERRED · 0% AMBIGUOUS · INFERRED: 242 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `5ea5fb0c`
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
- cn
- basic-info-section.tsx
- ChatSession
- PaymentService
- Context
- Trip
- Booking
- dependencies
- trip-form-screen.tsx
- OpenAITools
- trip-card.tsx
- AILog
- dependencies
- TripsListScreen
- trips-list-screen.tsx
- Context
- Context
- User
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
- data.ts
- repositories.go
- route.ts
- .EventStream
- tailwindcss
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
2. `New()` - 33 edges
3. `Trip` - 29 edges
4. `mockMCPRepo` - 24 edges
5. `ToolResult` - 24 edges
6. `ChatSession` - 21 edges
7. `Booking` - 21 edges
8. `AIService` - 21 edges
9. `MCPService` - 21 edges
10. `apiFetch()` - 21 edges

## Surprising Connections (you probably didn't know these)
- `buildTripFromRequest()` --calls--> `normalize()`  [INFERRED]
  backend/internal/services/trip_service.go → backend/internal/services/helpers.go
- `include` --extends--> `next-env.d.ts`  [EXTRACTED]
  backoffice-frontend/tsconfig.json → frontend/tsconfig.json
- `include` --extends--> `.next/types/**/*.ts`  [EXTRACTED]
  backoffice-frontend/tsconfig.json → frontend/tsconfig.json
- `main()` --calls--> `Connect()`  [INFERRED]
  backend/cmd/server/main.go → backend/internal/database/database.go
- `main()` --calls--> `NewBus()`  [INFERRED]
  backend/cmd/server/main.go → backend/internal/events/bus.go

## Import Cycles
- None detected.

## Communities (75 total, 12 thin omitted)

### Community 0 - "Success"
Cohesion: 0.07
Nodes (47): ClearGuestSessionCookie(), ClearRefreshCookie(), GetGuestSessionCookie(), GetRefreshCookie(), Context, parseSameSite(), SetGuestSessionCookie(), SetRefreshCookie() (+39 more)

### Community 1 - "backoffice-frontend/src/lib/api.ts"
Cohesion: 0.06
Nodes (62): geistMono, geistSans, metadata, AppShell(), AuthState, isPublicRoute(), publicRoutes, fetchPackages() (+54 more)

### Community 2 - "New"
Cohesion: 0.06
Nodes (49): Time, NewBus(), Context, UUID, NewAuditPool(), Context, T, ToolCall (+41 more)

### Community 3 - "main"
Cohesion: 0.06
Nodes (45): Claims, JWTService, TokenPair, main(), startChatSessionCleanup(), Duration, UUID, IsAudience() (+37 more)

### Community 4 - "dto.go"
Cohesion: 0.06
Nodes (39): B, LogSecurity(), UUID, Context, UUID, firstNonEmpty(), slugify(), BenchmarkSlugify() (+31 more)

### Community 5 - "ToolResult"
Cohesion: 0.10
Nodes (33): Message, ToolCall, Context, Handler, extractRecommendedPackages(), failedSearchTripsAlreadySelected(), formatAILogTrackingCode(), ChatMessage (+25 more)

### Community 6 - "MCPService"
Cohesion: 0.10
Nodes (29): Context, UUID, priceBreakdown(), tripAdultPrice(), tripChildPrice(), firstNonZero(), Time, limitSlice() (+21 more)

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
Cohesion: 0.11
Nodes (28): customerEmail(), customerName(), destination(), DetailDrawer(), filters, formatDate(), formatDateTime(), formatRelative() (+20 more)

### Community 11 - "ai_client.go"
Cohesion: 0.14
Nodes (26): Client, CompletionRequest, CompletionResponse, FunctionCall, FunctionSpec, ResponseFormat, ToolCall, ToolDef (+18 more)

### Community 12 - "mockMCPRepo"
Cohesion: 0.20
Nodes (6): ChatMessage, Context, Time, ToolCall, UUID, mockMCPRepo

### Community 13 - "use-trip-form.ts"
Cohesion: 0.16
Nodes (22): computeDiscountPercent(), formatDateForInput(), mapTripToForm(), normalizeAmenities(), normalizeCategory(), normalizeItineraries(), normalizeScheduleType(), parseDuration() (+14 more)

### Community 14 - "devDependencies"
Cohesion: 0.10
Nodes (24): devDependencies, eslint, eslint-config-next, postcss, @types/node, @types/react, @types/react-dom, typescript (+16 more)

### Community 15 - "form-section.tsx"
Cohesion: 0.15
Nodes (13): AmenitiesSection(), Props, HighlightsSection(), Props, ItinerarySection(), Props, Props, ReferenceSection() (+5 more)

### Community 16 - "Context"
Cohesion: 0.26
Nodes (5): ChatMessage, Context, Repository, Time, UUID

### Community 17 - "cn"
Cohesion: 0.18
Nodes (13): ConfirmModal(), ConfirmModalProps, CopyableField(), TripsToolbar(), TripsToolbarProps, ActivePanel, Category, ViewMode (+5 more)

### Community 18 - "basic-info-section.tsx"
Cohesion: 0.20
Nodes (10): BasicInfoSection(), Props, Props, SchedulingSection(), DateRange(), DurationPicker(), Field(), Label() (+2 more)

### Community 19 - "ChatSession"
Cohesion: 0.32
Nodes (12): ChatMessage, DB, Time, UUID, DeletedAt, AuthSession, BaseModel, ChatMessage (+4 more)

### Community 20 - "PaymentService"
Cohesion: 0.20
Nodes (8): IsPaymentSuccess(), NormalizePaymentStatus(), Context, UUID, PaymentWebhookRequest, PaymentRepository, PaymentRepository, PaymentService

### Community 21 - "Context"
Cohesion: 0.26
Nodes (4): Context, Repository, Time, UUID

### Community 22 - "Trip"
Cohesion: 0.29
Nodes (6): Context, Repository, UUID, Trip, TripMedia, TripRepositoryFilter

### Community 23 - "Booking"
Cohesion: 0.36
Nodes (4): Context, Repository, UUID, Booking

### Community 24 - "dependencies"
Cohesion: 0.15
Nodes (13): dependencies, clsx, lucide-react, next, react, react-dom, clsx, next (+5 more)

### Community 25 - "trip-form-screen.tsx"
Cohesion: 0.22
Nodes (6): ToastNotification(), ToastNotificationProps, ToastState, TripFormScreen(), InfoModal(), ModalType

### Community 26 - "OpenAITools"
Cohesion: 0.33
Nodes (10): ActiveCatalog(), Catalog(), OpenAITools(), requiredInputs(), T, TestOpenAITools_OnlyActiveToolsExposed(), TestOpenAITools_ParameterTypesNotForcedToString(), TestOpenAITools_RequiredArrays() (+2 more)

### Community 27 - "trip-card.tsx"
Cohesion: 0.32
Nodes (7): TripCard(), TripCardProps, formatDateRange(), formatTripPax(), getStatusTone(), formatIDR(), getDiscountMeta()

### Community 28 - "AILog"
Cohesion: 0.31
Nodes (5): Context, Repository, ToolCall, AILog, RepositoryFilter

### Community 29 - "dependencies"
Cohesion: 0.20
Nodes (10): tailwind-merge, dependencies, clsx, next, react, tailwind-merge, clsx, next (+2 more)

### Community 31 - "trips-list-screen.tsx"
Cohesion: 0.29
Nodes (5): CreateTripCard(), EmptyPackagesState(), OnDevelopmentPanel(), TripsSearchHeader(), TripsSearchHeaderProps

### Community 32 - "Context"
Cohesion: 0.36
Nodes (3): PaymentSuccessStatuses(), Context, Repository

### Community 33 - "Context"
Cohesion: 0.42
Nodes (3): Context, Repository, UUID

### Community 34 - "User"
Cohesion: 0.39
Nodes (5): Context, Repository, UUID, Role, User

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

### Community 45 - "data.ts"
Cohesion: 0.40
Nodes (4): orders, payments, travelCards, workflowSteps

### Community 46 - "repositories.go"
Cohesion: 0.83
Nodes (3): DB, Repository, New()

### Community 49 - "tailwindcss"
Cohesion: 0.67
Nodes (3): tailwindcss, tailwindcss, tailwindcss

## Knowledge Gaps
- **133 isolated node(s):** `github.com/rozzi/vero-ai-travel-agents/backend`, `RefreshRequest`, `ChatRecommendationReason`, `UploadResponse`, `Handler` (+128 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **12 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `New` to `Success`, `main`, `dto.go`, `ToolResult`, `MCPService`, `Services`, `ai_client.go`, `mockMCPRepo`, `PaymentService`?**
  _High betweenness centrality (0.058) - this node is a cross-community bridge._
- **Why does `Config` connect `Success` to `New`, `main`, `dto.go`, `ToolResult`, `Services`, `PaymentService`?**
  _High betweenness centrality (0.049) - this node is a cross-community bridge._
- **Why does `Services` connect `Services` to `Success`, `New`, `main`, `dto.go`, `ToolResult`, `MCPService`, `PaymentService`?**
  _High betweenness centrality (0.044) - this node is a cross-community bridge._
- **Are the 32 inferred relationships involving `Success()` (e.g. with `respondAuthIssue()` and `.AdminCreateUser()`) actually correct?**
  _`Success()` has 32 INFERRED edges - model-reasoned connections that need verification._
- **Are the 27 inferred relationships involving `New()` (e.g. with `TestAuditPool_SubmitAndDrain()` and `TestAuditPool_SubmitNonBlockingWhenFull()`) actually correct?**
  _`New()` has 27 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/rozzi/vero-ai-travel-agents/backend`, `RefreshRequest`, `ChatRecommendationReason` to the rest of the system?**
  _133 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Success` be split into smaller, more focused modules?**
  _Cohesion score 0.06613891726251277 - nodes in this community are weakly interconnected._