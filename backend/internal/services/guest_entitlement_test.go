package services

import (
	"testing"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// Normalization is what decides whether two spellings of the same contact
// collapse to one anchor (GO-P0-1). These cases lock the equivalences the guest
// entitlement depends on — a regression here silently hands out extra orders.

func TestNormalizeGuestContactEmail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"trim and lowercase", "  Guest.Order@Example.COM ", "guest.order@example.com"},
		{"strip plus tag", "guest.order+order2@example.com", "guest.order@example.com"},
		{"keep dots", "g.u.e.s.t@example.com", "g.u.e.s.t@example.com"},
		{"subdomain kept", "guest@mail.example.co.id", "guest@mail.example.co.id"},
		{"missing at", "not-an-address", ""},
		{"empty local part", "@example.com", ""},
		{"empty domain", "guest@", ""},
		{"only plus tag local", "+tag@example.com", "+tag@example.com"},
		{"blank", "   ", ""},
	}
	for _, tc := range cases {
		if got := normalizeGuestContactEmail(tc.in); got != tc.want {
			t.Errorf("%s: normalizeGuestContactEmail(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestNormalizeGuestContactPhone(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"local trunk prefix", "0812-3456-789", "628123456789"},
		{"international plus", "+62 812 3456 789", "628123456789"},
		{"international access code", "0062 812 3456 789", "628123456789"},
		{"already normalized", "628123456789", "628123456789"},
		{"parentheses and spaces", "(0812) 3456 789", "628123456789"},
		{"no digits", "no-digits-here", ""},
		{"only zeros", "0000", ""},
		{"blank", "", ""},
	}
	for _, tc := range cases {
		if got := normalizeGuestContactPhone(tc.in); got != tc.want {
			t.Errorf("%s: normalizeGuestContactPhone(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestGuestContactAnchors(t *testing.T) {
	both := guestContactAnchors(dto.BookingRequest{ContactEmail: "Guest@Example.com", ContactPhone: "0812-3456-789"})
	if len(both) != 2 {
		t.Fatalf("expected email + phone anchors, got %d", len(both))
	}
	if both[0].Channel != models.GuestContactChannelEmail || both[1].Channel != models.GuestContactChannelPhone {
		t.Fatalf("unexpected channels: %s, %s", both[0].Channel, both[1].Channel)
	}
	if both[0].Key == both[1].Key {
		t.Fatal("email and phone anchors must never share a key")
	}

	// Equivalent spellings must produce identical keys...
	same := guestContactAnchors(dto.BookingRequest{ContactEmail: " guest+two@example.com", ContactPhone: "+62 812 3456 789"})
	if len(same) != 2 || same[0].Key != both[0].Key || same[1].Key != both[1].Key {
		t.Fatal("equivalent contact spellings must map to the same anchors")
	}
	// ...and a different contact must not.
	other := guestContactAnchors(dto.BookingRequest{ContactEmail: "someone@example.com", ContactPhone: "081999888777"})
	if other[0].Key == both[0].Key || other[1].Key == both[1].Key {
		t.Fatal("different contacts must map to different anchors")
	}

	if got := guestContactAnchors(dto.BookingRequest{ContactEmail: "nope", ContactPhone: "n/a"}); len(got) != 0 {
		t.Fatalf("unusable contact must yield no anchor, got %d", len(got))
	}
	if keys := guestContactKeys(both); len(keys) != 2 || keys[0] != both[0].Key || keys[1] != both[1].Key {
		t.Fatal("guestContactKeys must project the anchor keys in order")
	}
}
