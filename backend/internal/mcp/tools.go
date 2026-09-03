package mcp

import "github.com/rozzi/vero-ai-travel-agents/backend/internal/ai"

// JSON Schema primitive types used to declare tool parameters (AI-2).
// Declaring accurate types (instead of a blanket "string") prevents the LLM
// from hallucinating parameter types when planning tool calls.
const (
	ParamTypeString  = "string"
	ParamTypeInteger = "integer"
	ParamTypeBoolean = "boolean"
	ParamTypeNumber  = "number"
)

const (
	ToolSearchTrips        = "search_trips"
	ToolSelectPackage      = "select_package"
	ToolCollectOrderDetail = "collect_order_detail"
	ToolCreateBooking      = "create_booking"
	ToolCreateOrder        = "create_order"

	// AIW-5: richer catalog/pricing/availability tools so the LLM grounds its
	// answers in backend data instead of guessing. Backend stays the source of
	// truth for pricing (calculate_trip_price reuses the booking pricing logic)
	// and availability (check_trip_availability reads catalog schedule/quota).
	ToolGetTripDetail         = "get_trip_detail"
	ToolCalculateTripPrice    = "calculate_trip_price"
	ToolCheckTripAvailability = "check_trip_availability"
	// ToolCheckOrderStatus lets the AI answer "is my order created / what's my
	// order id" for THIS chat session, and lets create_booking block duplicate
	// orders (AIW-8). Session-scoped: it never looks up other sessions' orders.
	ToolCheckOrderStatus = "check_order_status"

	// Legacy tools removed from OpenAI tool catalog. They are no longer exposed
	// to the LLM because search_trips is now the single source of package
	// recommendations. Kept as constants for compatibility with internal logging.
	ToolSearchDestination = "search_destination"
	ToolSearchHotels      = "search_hotels"
	ToolCalculateBudget   = "calculate_budget"
	ToolGenerateItinerary = "generate_itinerary"
	ToolCreatePayment     = "create_payment"
	ToolSendWhatsApp      = "send_whatsapp"
	ToolUpdateOrderDraft  = "update_order_draft"
)

// InputDefinition declares one named tool parameter together with the JSON
// Schema type the LLM must supply for it (AI-2).
type InputDefinition struct {
	Name string `json:"name"`
	// Type is one of ParamTypeString / ParamTypeInteger / ParamTypeBoolean /
	// ParamTypeNumber. Empty means ParamTypeString (backward compatible).
	Type string `json:"type"`
}

// ToolDefinition describes an MCP tool that the AI orchestration layer can call.
// Enabled indicates whether the tool participates in the live chat workflow.
// Inputs carries per-parameter names plus their JSON Schema types so that
// OpenAITools() can emit an accurate schema instead of forcing every argument
// to be a string (AI-2 hallucination guard).
type ToolDefinition struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Inputs      []InputDefinition `json:"inputs"`
	Enabled     bool              `json:"enabled"`
}

// Catalog returns every MCP tool known to the platform. Only the minimal set
// required for the chat recommendation flow is enabled.
func Catalog() []ToolDefinition {
	return []ToolDefinition{
		// AI-2: per-parameter types ride on Inputs so the LLM sees integer for
		// pax, boolean for alternative, number for cost, etc., reducing
		// schema hallucination. Downstream parsing still stays defensive
		// (strings, ints, bools all tolerated).
		{Name: ToolSearchTrips, Description: "Find and recommend published travel packages from the catalog based on the user's query. Use this to show packages, respond to requests like 'cari paket', or show alternatives. Pass alternative=true only when the user explicitly asks for other options while a package is already selected.", Inputs: []InputDefinition{{Name: "query", Type: ParamTypeString}, {Name: "alternative", Type: ParamTypeBoolean}}, Enabled: true},
		{Name: ToolSelectPackage, Description: "Mark a package as selected by the user. Call this when the user explicitly chooses a package by name or ID. Once a package is selected, stop recommending other packages unless the user asks for alternatives.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}}, Enabled: true},
		{Name: ToolCollectOrderDetail, Description: "Record order details collected from the user (pax, travel date, contact). Call this while gathering information before creating the actual booking. Does NOT create an order.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}, {Name: "adult_pax", Type: ParamTypeInteger}, {Name: "child_pax", Type: ParamTypeInteger}, {Name: "travel_date", Type: ParamTypeString}, {Name: "contact_name", Type: ParamTypeString}, {Name: "contact_email", Type: ParamTypeString}, {Name: "contact_phone", Type: ParamTypeString}}, Enabled: true},
		{Name: ToolCreateBooking, Description: "Create the final order in the database. Call this only when all required info is complete: trip_id, adult_pax, child_pax, travel_date, and contact_email OR contact_phone. The contact must be the real one the user gave — an email containing @ or a phone number with digits — never a placeholder, because the order is anchored to it. Returns success=true with order_id only after database save succeeds.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}, {Name: "adult_pax", Type: ParamTypeInteger}, {Name: "child_pax", Type: ParamTypeInteger}, {Name: "travel_date", Type: ParamTypeString}, {Name: "contact_name", Type: ParamTypeString}, {Name: "contact_email", Type: ParamTypeString}, {Name: "contact_phone", Type: ParamTypeString}}, Enabled: true},
		{Name: ToolCreateOrder, Description: "Alias of create_booking. Disabled from OpenAI catalog to prevent model confusion.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}, {Name: "adult_pax", Type: ParamTypeInteger}, {Name: "child_pax", Type: ParamTypeInteger}, {Name: "travel_date", Type: ParamTypeString}, {Name: "contact_name", Type: ParamTypeString}, {Name: "contact_email", Type: ParamTypeString}, {Name: "contact_phone", Type: ParamTypeString}}, Enabled: false},

		// AIW-5: detail / pricing / availability tools. These ground the LLM's
		// answers in backend data (source of truth) instead of hallucination.
		{Name: ToolGetTripDetail, Description: "Get full detail of ONE package: basic info, adult/child prices, discount info, itinerary (daily plan), included/excluded amenities, pax quota, references, and media. Call this when the user asks for details of a specific package (itinerary, fasilitas, apa saja yang termasuk, harga anak, diskon). Requires trip_id.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}}, Enabled: true},
		{Name: ToolCalculateTripPrice, Description: "Calculate the authoritative total price for a booking from the backend catalog (source of truth). ALWAYS use this to quote a total; NEVER compute a total yourself. Returns per-unit adult/child prices, active discounts, quantities, and the final total. Requires trip_id, adult_pax, child_pax.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}, {Name: "adult_pax", Type: ParamTypeInteger}, {Name: "child_pax", Type: ParamTypeInteger}}, Enabled: true},
		{Name: ToolCheckTripAvailability, Description: "Check whether a package is available for a given travel date and pax count, based on backend catalog schedule/quota (source of truth). ALWAYS use this before confirming a date is available; NEVER guarantee availability from catalog data alone. Requires trip_id, travel_date (YYYY-MM-DD), adult_pax, child_pax.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}, {Name: "travel_date", Type: ParamTypeString}, {Name: "adult_pax", Type: ParamTypeInteger}, {Name: "child_pax", Type: ParamTypeInteger}}, Enabled: true},
		{Name: ToolCheckOrderStatus, Description: "Check whether an order has already been created in THIS chat session, and return its order id, status, and total. Call this when the user asks if their order is ready/created, asks for their order number, or before re-creating an order. Takes no parameters (uses the current session).", Inputs: []InputDefinition{}, Enabled: true},

		// Legacy mock tools — disabled from the OpenAI catalog.
		{Name: ToolSearchDestination, Description: "Legacy tool.", Inputs: []InputDefinition{{Name: "prompt", Type: ParamTypeString}, {Name: "budget", Type: ParamTypeNumber}, {Name: "season", Type: ParamTypeString}}, Enabled: false},
		{Name: ToolSearchHotels, Description: "Legacy tool.", Inputs: []InputDefinition{{Name: "destination", Type: ParamTypeString}, {Name: "dates", Type: ParamTypeString}, {Name: "tier", Type: ParamTypeString}}, Enabled: false},
		{Name: ToolCalculateBudget, Description: "Legacy tool.", Inputs: []InputDefinition{{Name: "destination", Type: ParamTypeString}, {Name: "duration", Type: ParamTypeString}, {Name: "travelers", Type: ParamTypeInteger}}, Enabled: false},
		{Name: ToolGenerateItinerary, Description: "Legacy tool.", Inputs: []InputDefinition{{Name: "destination", Type: ParamTypeString}, {Name: "duration", Type: ParamTypeString}, {Name: "interests", Type: ParamTypeString}}, Enabled: false},
		{Name: ToolUpdateOrderDraft, Description: "Legacy tool.", Inputs: []InputDefinition{{Name: "trip_id", Type: ParamTypeString}, {Name: "adult_pax", Type: ParamTypeInteger}, {Name: "child_pax", Type: ParamTypeInteger}, {Name: "contact_name", Type: ParamTypeString}, {Name: "contact_email", Type: ParamTypeString}, {Name: "contact_phone", Type: ParamTypeString}, {Name: "travel_date", Type: ParamTypeString}}, Enabled: false},
		{Name: ToolCreatePayment, Description: "Create QRIS or Virtual Account payment intent. Disabled while DOKU payment flow is temporarily off.", Inputs: []InputDefinition{{Name: "booking_id", Type: ParamTypeString}, {Name: "amount", Type: ParamTypeNumber}, {Name: "method", Type: ParamTypeString}}, Enabled: false},
		{Name: ToolSendWhatsApp, Description: "Trigger WhatsApp confirmation automation. Defined for future use, not yet part of the live workflow.", Inputs: []InputDefinition{{Name: "phone", Type: ParamTypeString}, {Name: "message", Type: ParamTypeString}}, Enabled: false},
	}
}

// ActiveCatalog returns only the tools that are currently part of the live
// AI chat workflow.
func ActiveCatalog() []ToolDefinition {
	all := Catalog()
	active := make([]ToolDefinition, 0, len(all))
	for _, tool := range all {
		if tool.Enabled {
			active = append(active, tool)
		}
	}
	return active
}

// OpenAITools converts the active MCP catalog into OpenAI-compatible tool
// definitions that can be sent in the chat completions request.
func OpenAITools() []ai.ToolDef {
	active := ActiveCatalog()
	defs := make([]ai.ToolDef, 0, len(active))
	for _, tool := range active {
		props := make(map[string]interface{}, len(tool.Inputs))
		for _, input := range tool.Inputs {
			inputType := input.Type
			if inputType == "" {
				inputType = ParamTypeString
			}
			props[input.Name] = map[string]interface{}{
				"type":        inputType,
				"description": input.Name,
			}
		}
		required := requiredInputs(tool)
		defs = append(defs, ai.ToolDef{
			Type: "function",
			Function: ai.FunctionSpec{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": props,
					"required":   required,
				},
			},
		})
	}
	return defs
}

func requiredInputs(tool ToolDefinition) []string {
	switch tool.Name {
	case ToolCreateBooking, ToolCreateOrder:
		return []string{"trip_id", "adult_pax", "child_pax", "travel_date"}
	case ToolSelectPackage:
		return []string{"trip_id"}
	case ToolGetTripDetail:
		return []string{"trip_id"}
	case ToolCalculateTripPrice:
		return []string{"trip_id", "adult_pax", "child_pax"}
	case ToolCheckTripAvailability:
		return []string{"trip_id", "travel_date"}
	case ToolCollectOrderDetail:
		return []string{"trip_id"}
	case ToolSearchTrips:
		return []string{"query"}
	default:
		names := make([]string, 0, len(tool.Inputs))
		for _, input := range tool.Inputs {
			names = append(names, input.Name)
		}
		return names
	}
}
