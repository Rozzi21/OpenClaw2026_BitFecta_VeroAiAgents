package services

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	value = strings.Trim(re.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return uuid.NewString()
	}
	return value + "-" + uuid.NewString()[:8]
}

func normalize(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ParseIntFromString(s string, fallback int) int {
	parsed, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func sanitizePromptInjection(s string) string {
	// Strip characters/words often used in prompt injection.
	// Also clean potential HTML/JS tags, and common command override words.
	lower := strings.ToLower(s)
	if strings.Contains(lower, "ignore previous instructions") ||
		strings.Contains(lower, "abaikan instruksi") ||
		strings.Contains(lower, "system prompt") {
		// Neutralize known injection phrases
		s = strings.ReplaceAll(s, "ignore previous instructions", "[removed phrase]")
		s = strings.ReplaceAll(s, "Ignore previous instructions", "[removed phrase]")
		s = strings.ReplaceAll(s, "abaikan instruksi", "[removed phrase]")
		s = strings.ReplaceAll(s, "Abaikan instruksi", "[removed phrase]")
	}
	// Limit special control characters that might confuse delimiters
	s = strings.ReplaceAll(s, "`", "'")
	s = strings.ReplaceAll(s, "<", "[")
	s = strings.ReplaceAll(s, ">", "]")
	return s
}

func limitString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}

func limitSlice(slice []string, maxItems int) []string {
	if len(slice) > maxItems {
		return slice[:maxItems]
	}
	return slice
}

func parseDate(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	// Try multiple layouts, including common natural layouts and ISO formats
	layouts := []string{
		time.RFC3339,
		"2006-01-02",
		"02-01-2006",
		"02/01/2006",
		"2006/01/02",
		"2006.01.02",
		"02 Jan 2006",
		"02 January 2006",
		"January 02, 2006",
		"Jan 02, 2006",
	}

	// Normalize Indonesian month names if any
	val := strings.ToLower(value)
	val = strings.ReplaceAll(val, "januari", "january")
	val = strings.ReplaceAll(val, "februari", "february")
	val = strings.ReplaceAll(val, "maret", "march")
	val = strings.ReplaceAll(val, "april", "april")
	val = strings.ReplaceAll(val, "mei", "may")
	val = strings.ReplaceAll(val, "juni", "june")
	val = strings.ReplaceAll(val, "juli", "july")
	val = strings.ReplaceAll(val, "agustus", "august")
	val = strings.ReplaceAll(val, "september", "september")
	val = strings.ReplaceAll(val, "oktober", "october")
	val = strings.ReplaceAll(val, "november", "november")
	val = strings.ReplaceAll(val, "desember", "december")

	val = strings.ReplaceAll(val, "jan", "jan")
	val = strings.ReplaceAll(val, "feb", "feb")
	val = strings.ReplaceAll(val, "mar", "mar")
	val = strings.ReplaceAll(val, "apr", "apr")
	val = strings.ReplaceAll(val, "mei", "may")
	val = strings.ReplaceAll(val, "jun", "jun")
	val = strings.ReplaceAll(val, "jul", "jul")
	val = strings.ReplaceAll(val, "agu", "aug")
	val = strings.ReplaceAll(val, "sep", "sep")
	val = strings.ReplaceAll(val, "okt", "oct")
	val = strings.ReplaceAll(val, "nov", "nov")
	val = strings.ReplaceAll(val, "des", "dec")

	for _, layout := range layouts {
		parsed, err := time.Parse(layout, val)
		if err == nil {
			return &parsed
		}
		// Try case-insensitive matching by parsing with capitalized names
		parsedTitle, errTitle := time.Parse(layout, strings.Title(val))
		if errTitle == nil {
			return &parsedTitle
		}
	}
	return nil
}
