package mcp

import (
	"testing"
)

// TestOpenAITools_ParameterTypesNotForcedToString is the regression test for
// AI-2 (Deklarasi Tipe Parameter Fungsi LLM Selalu "String" / Hallucination
// Risk). Before the fix, OpenAITools() forced every parameter to
// type:"string", so integer pax and the boolean alternative flag were declared
// as strings — confusing Structured Outputs LLMs. This test locks the
// per-parameter JSON Schema type emitted for each active tool so a regression
// (e.g. someone re-introducing a blanket "string") fails the build.
func TestOpenAITools_ParameterTypesNotForcedToString(t *testing.T) {
	tools := OpenAITools()

	// Build tool -> param -> declared type map for fast lookup.
	schema := make(map[string]map[string]string, len(tools))
	for _, td := range tools {
		params, ok := td.Function.Parameters.(map[string]interface{})
		if !ok {
			t.Fatalf("tool %s: parameters not a map[string]interface{}", td.Function.Name)
		}
		props, ok := params["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("tool %s: properties missing", td.Function.Name)
		}
		propTypes := make(map[string]string, len(props))
		for name, raw := range props {
			propMap, ok := raw.(map[string]interface{})
			if !ok {
				t.Fatalf("tool %s prop %s: not a map", td.Function.Name, name)
			}
			typ, _ := propMap["type"].(string)
			if typ == "" {
				typ = ParamTypeString
			}
			propTypes[name] = typ
		}
		schema[td.Function.Name] = propTypes
	}

	cases := []struct {
		tool    string
		param   string
		wantTyp string
	}{
		{ToolSearchTrips, "query", ParamTypeString},
		{ToolSearchTrips, "alternative", ParamTypeBoolean},
		{ToolSelectPackage, "trip_id", ParamTypeString},
		{ToolCollectOrderDetail, "trip_id", ParamTypeString},
		{ToolCollectOrderDetail, "adult_pax", ParamTypeInteger},
		{ToolCollectOrderDetail, "child_pax", ParamTypeInteger},
		{ToolCollectOrderDetail, "travel_date", ParamTypeString},
		{ToolCollectOrderDetail, "contact_name", ParamTypeString},
		{ToolCreateBooking, "trip_id", ParamTypeString},
		{ToolCreateBooking, "adult_pax", ParamTypeInteger},
		{ToolCreateBooking, "child_pax", ParamTypeInteger},
		{ToolCreateBooking, "travel_date", ParamTypeString},
		{ToolCreateBooking, "contact_email", ParamTypeString},
		{ToolCreateBooking, "contact_phone", ParamTypeString},
	}
	for _, c := range cases {
		propTypes, ok := schema[c.tool]
		if !ok {
			t.Errorf("active catalog missing tool %q (AI-2 regression: tool dropped?)", c.tool)
			continue
		}
		got, ok := propTypes[c.param]
		if !ok {
			t.Errorf("tool %q missing param %q (AI-2 regression)", c.tool, c.param)
			continue
		}
		if got != c.wantTyp {
			t.Errorf("tool %q param %q: type = %q, want %q (AI-2 regression: blanket string?)",
				c.tool, c.param, got, c.wantTyp)
		}
	}

	// Hard guard: no active tool may declare ANY parameter as the blanket
	// "string" when the InputDefinition specifies a non-string type. We already
	// assert specific non-string params above; here we additionally assert the
	// two known integer params are never string, which is the core of AI-2.
	for _, tool := range tools {
		propTypes := schema[tool.Function.Name]
		for _, intParam := range []string{"adult_pax", "child_pax"} {
			if typ, ok := propTypes[intParam]; ok && typ == ParamTypeString {
				t.Errorf("tool %q param %q declared string, want integer (AI-2 regression)",
					tool.Function.Name, intParam)
			}
		}
		if typ, ok := propTypes["alternative"]; ok && typ == ParamTypeString {
			t.Errorf("tool %q param alternative declared string, want boolean (AI-2 regression)",
				tool.Function.Name)
		}
	}
}

// TestOpenAITools_RequiredArrays sanity-checks the required-input mapping stays
// intact alongside the type fix (regression guard for requiredInputs).
func TestOpenAITools_RequiredArrays(t *testing.T) {
	tools := OpenAITools()
	requiredByTool := make(map[string][]string, len(tools))
	for _, td := range tools {
		params, ok := td.Function.Parameters.(map[string]interface{})
		if !ok {
			t.Fatalf("tool %s: parameters not a map", td.Function.Name)
		}
		raw, ok := params["required"].([]string)
		if !ok {
			// Could be []interface{} depending on literal; coerce.
			ifaceSlice, ok2 := params["required"].([]interface{})
			if !ok2 {
				t.Fatalf("tool %s: required not a slice", td.Function.Name)
			}
			raw = make([]string, 0, len(ifaceSlice))
			for _, v := range ifaceSlice {
				s, _ := v.(string)
				raw = append(raw, s)
			}
		}
		requiredByTool[td.Function.Name] = raw
	}

	want := map[string][]string{
		ToolCreateBooking:      {"trip_id", "adult_pax", "child_pax", "travel_date"},
		ToolSelectPackage:      {"trip_id"},
		ToolCollectOrderDetail: {"trip_id"},
		ToolSearchTrips:        {"query"},
	}
	for tool, wantReq := range want {
		gotReq, ok := requiredByTool[tool]
		if !ok {
			t.Errorf("tool %q missing from active catalog", tool)
			continue
		}
		if len(gotReq) != len(wantReq) {
			t.Errorf("tool %q required len = %d, want %d (%v vs %v)", tool, len(gotReq), len(wantReq), gotReq, wantReq)
			continue
		}
		for i := range wantReq {
			if gotReq[i] != wantReq[i] {
				t.Errorf("tool %q required[%d] = %q, want %q", tool, i, gotReq[i], wantReq[i])
			}
		}
	}
}

// TestOpenAITools_OnlyActiveToolsExposed ensures disabled tools (create_order,
// create_payment, send_whatsapp, legacy mocks) never leak into the OpenAI
// catalog. This protects both AIW-5 (create_order alias disabled) and the
// general Enabled contract.
func TestOpenAITools_OnlyActiveToolsExposed(t *testing.T) {
	tools := OpenAITools()
	disabled := map[string]bool{
		ToolCreateOrder:       true,
		ToolCreatePayment:     true,
		ToolSendWhatsApp:      true,
		ToolSearchDestination: true,
		ToolSearchHotels:      true,
		ToolCalculateBudget:   true,
		ToolGenerateItinerary: true,
		ToolUpdateOrderDraft:  true,
	}
	for _, td := range tools {
		if disabled[td.Function.Name] {
			t.Errorf("disabled tool %q leaked into OpenAITools() catalog", td.Function.Name)
		}
	}
	// The four live workflow tools must be present.
	wantActive := []string{ToolSearchTrips, ToolSelectPackage, ToolCollectOrderDetail, ToolCreateBooking}
	present := make(map[string]bool, len(tools))
	for _, td := range tools {
		present[td.Function.Name] = true
	}
	for _, name := range wantActive {
		if !present[name] {
			t.Errorf("active tool %q missing from OpenAITools() catalog", name)
		}
	}
}
