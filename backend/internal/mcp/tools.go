package mcp

import "github.com/rozzi/vero-ai-travel-agents/backend/internal/ai"

const (
	ToolSearchTrips        = "search_trips"
	ToolSelectPackage      = "select_package"
	ToolCollectOrderDetail = "collect_order_detail"
	ToolCreateBooking      = "create_booking"
	ToolCreateOrder        = "create_order"

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

// ToolDefinition describes an MCP tool that the AI orchestration layer can call.
// Enabled indicates whether the tool participates in the live chat workflow.
type ToolDefinition struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Inputs      []string `json:"inputs"`
	Enabled     bool     `json:"enabled"`
}

// Catalog returns every MCP tool known to the platform. Only the minimal set
// required for the chat recommendation flow is enabled.
func Catalog() []ToolDefinition {
	return []ToolDefinition{
		{Name: ToolSearchTrips, Description: "Find and recommend published travel packages from the catalog based on the user's query. Use this to show packages, respond to requests like 'cari paket', or show alternatives. Pass alternative=true only when the user explicitly asks for other options while a package is already selected.", Inputs: []string{"query", "alternative"}, Enabled: true},
		{Name: ToolSelectPackage, Description: "Mark a package as selected by the user. Call this when the user explicitly chooses a package by name or ID. Once a package is selected, stop recommending other packages unless the user asks for alternatives.", Inputs: []string{"trip_id"}, Enabled: true},
		{Name: ToolCollectOrderDetail, Description: "Record order details collected from the user (pax, travel date, contact). Call this while gathering information before creating the actual booking. Does NOT create an order.", Inputs: []string{"trip_id", "adult_pax", "child_pax", "travel_date", "contact_name", "contact_email", "contact_phone"}, Enabled: true},
		{Name: ToolCreateBooking, Description: "Create the final order in the database. Call this only when all required info is complete: trip_id, adult_pax, child_pax, travel_date, and contact_email OR contact_phone. Returns success=true with order_id only after database save succeeds.", Inputs: []string{"trip_id", "adult_pax", "child_pax", "travel_date", "contact_name", "contact_email", "contact_phone"}, Enabled: true},
		{Name: ToolCreateOrder, Description: "Alias of create_booking. Disabled from OpenAI catalog to prevent model confusion.", Inputs: []string{"trip_id", "adult_pax", "child_pax", "travel_date", "contact_name", "contact_email", "contact_phone"}, Enabled: false},

		// Legacy mock tools — disabled from the OpenAI catalog.
		{Name: ToolSearchDestination, Description: "Legacy tool.", Inputs: []string{"prompt", "budget", "season"}, Enabled: false},
		{Name: ToolSearchHotels, Description: "Legacy tool.", Inputs: []string{"destination", "dates", "tier"}, Enabled: false},
		{Name: ToolCalculateBudget, Description: "Legacy tool.", Inputs: []string{"destination", "duration", "travelers"}, Enabled: false},
		{Name: ToolGenerateItinerary, Description: "Legacy tool.", Inputs: []string{"destination", "duration", "interests"}, Enabled: false},
		{Name: ToolUpdateOrderDraft, Description: "Legacy tool.", Inputs: []string{"trip_id", "adult_pax", "child_pax", "contact_name", "contact_email", "contact_phone", "travel_date"}, Enabled: false},
		{Name: ToolCreatePayment, Description: "Create QRIS or Virtual Account payment intent. Disabled while DOKU payment flow is temporarily off.", Inputs: []string{"booking_id", "amount", "method"}, Enabled: false},
		{Name: ToolSendWhatsApp, Description: "Trigger WhatsApp confirmation automation. Defined for future use, not yet part of the live workflow.", Inputs: []string{"phone", "message"}, Enabled: false},
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
			props[input] = map[string]interface{}{
				"type":        "string",
				"description": input,
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
	case ToolCollectOrderDetail:
		return []string{"trip_id"}
	case ToolSearchTrips:
		return []string{"query"}
	default:
		return tool.Inputs
	}
}
