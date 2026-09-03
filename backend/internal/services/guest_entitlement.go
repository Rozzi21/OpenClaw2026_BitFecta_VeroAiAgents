package services

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// Guest one-order entitlement — contact anchors (GO-P0-1).
//
// The guest allowance used to live only in guest_sessions.order_count, keyed by
// the vero_guest_session cookie. Enforcement was atomic, but the KEY was chosen
// by the client: dropping the cookie made GuestService.Resolve mint a new
// identity with a fresh allowance, so the rule was effectively "one order per
// cookie the visitor chooses to keep" instead of "one order per unauthenticated
// visitor".
//
// The anchors below add a second key the client does not get to pick: the
// contact channel the order is placed with, normalized so trivial rewrites of
// the same address/number collapse to one key. They are stored hashed in
// guest_order_entitlements (unique index on contact_key) and consumed inside
// the booking transaction, which makes the database — not the browser, the
// frontend, localStorage, the ChatSession, the AI/MCP layer, or the request IP
// — the authority for "this visitor already ordered".
//
// Deliberate scope limits (documented, not oversights):
//   - Only guest orders write anchors, and only guest orders read them.
//     Authenticated bookings keep the normal booking rules.
//   - A visitor who supplies a genuinely different email AND phone still gets
//     one order. Closing that needs verified contacts (OTP), which is a product
//     decision, not a backend one — see docs/GUEST_ORDER_AUDIT.md GO-P0-1 (d).

// Reasons attached to the guest_order_limit_reached audit event so abuse of one
// anchor can be told apart from the other. Kept as stable category strings
// (coding-rules §1.6: audit payloads carry categories, never raw errors).
const (
	guestLimitReasonSessionSpent = "guest_session_spent"
	guestLimitReasonContactSpent = "contact_already_used"
)

// guestContactAnchor is one normalized contact channel of a booking request.
// Key is the value actually persisted/compared; the plaintext never leaves this
// package.
type guestContactAnchor struct {
	Channel string
	Key     string
}

// guestContactAnchors derives every usable anchor from a booking request. A
// request may yield two anchors (email + phone); either one being already spent
// blocks the order, and both are consumed on success so the visitor cannot come
// back with only one of them.
func guestContactAnchors(req dto.BookingRequest) []guestContactAnchor {
	anchors := make([]guestContactAnchor, 0, 2)
	if email := normalizeGuestContactEmail(req.ContactEmail); email != "" {
		anchors = append(anchors, guestContactAnchor{
			Channel: models.GuestContactChannelEmail,
			Key:     hashGuestContact(models.GuestContactChannelEmail, email),
		})
	}
	if phone := normalizeGuestContactPhone(req.ContactPhone); phone != "" {
		anchors = append(anchors, guestContactAnchor{
			Channel: models.GuestContactChannelPhone,
			Key:     hashGuestContact(models.GuestContactChannelPhone, phone),
		})
	}
	return anchors
}

// guestContactKeys projects the anchors to the keys used for the lookup.
func guestContactKeys(anchors []guestContactAnchor) []string {
	keys := make([]string, 0, len(anchors))
	for _, anchor := range anchors {
		keys = append(keys, anchor.Key)
	}
	return keys
}

// guestOrderEntitlements builds the rows persisted once a guest order is
// created. GuestSessionID is kept for auditing only — the entitlement stays
// valid after the guest session rotates or expires, which is the whole point.
func guestOrderEntitlements(anchors []guestContactAnchor, guestID, bookingID uuid.UUID) []models.GuestOrderEntitlement {
	owner := guestID
	rows := make([]models.GuestOrderEntitlement, 0, len(anchors))
	for _, anchor := range anchors {
		rows = append(rows, models.GuestOrderEntitlement{
			ContactKey:     anchor.Key,
			Channel:        anchor.Channel,
			GuestSessionID: &owner,
			BookingID:      bookingID,
		})
	}
	return rows
}

// hashGuestContact digests "<channel>:<normalized value>". The channel is part
// of the pre-image so an email can never collide with a phone number, and the
// digest keeps a second copy of the contact PII out of the entitlement table
// (same rationale as guest_sessions.token_hash).
func hashGuestContact(channel, normalized string) string {
	sum := sha256.Sum256([]byte(channel + ":" + normalized))
	return hex.EncodeToString(sum[:])
}

// normalizeGuestContactEmail lowercases, trims and strips the "+tag" suffix of
// the local part, so "Guest@Example.com ", "guest@example.com" and
// "guest+order2@example.com" all anchor to the same key. Dots are NOT stripped:
// that is Gmail-specific and would wrongly merge distinct mailboxes elsewhere.
// Returns "" when the value cannot be an address (no "@", empty local part or
// empty domain) — the caller then treats the request as having no email anchor.
func normalizeGuestContactEmail(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return ""
	}
	local, domain := value[:at], value[at+1:]
	if plus := strings.Index(local, "+"); plus > 0 {
		local = local[:plus]
	}
	if local == "" {
		return ""
	}
	return local + "@" + domain
}

// normalizeGuestContactPhone reduces a phone number to digits and folds the
// prefixes Indonesian customers type interchangeably, so "0812-3456-789",
// "+62 812 3456 789", "0062 812 3456 789" and "628123456789" all anchor to the
// same key. Returns "" when there is no digit to anchor on.
//
// Known limitation: the trunk-prefix fold assumes Indonesian numbering (the
// product's market). A foreign number written in local form is folded too, so
// its two spellings ("+44 20 …" vs "020 …") produce different keys. That can
// only miss a block, never create one for a different person.
func normalizeGuestContactPhone(raw string) string {
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	value := strings.TrimPrefix(digits.String(), "00")
	if strings.HasPrefix(value, "0") {
		trimmed := strings.TrimLeft(value, "0")
		if trimmed == "" {
			return ""
		}
		value = "62" + trimmed
	}
	return value
}
