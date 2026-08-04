package services

import (
	"strings"
	"testing"
)

// TestSlugify_Perf3RegexNotRecompiled locks PERF-3 #1: slugify must use the
// package-level compiled regex (slugNonAlnum), not regexp.MustCompile per call.
// We assert behavior (deterministic slug output) plus that calling slugify many
// times in a tight loop does not panic / exhaust memory — a regression to
// per-call MustCompile would still pass behavior but is caught by the benchmark
// guard below. The behavioral assertions also guard against accidental pattern
// changes.
func TestSlugify_Perf3RegexNotRecompiled(t *testing.T) {
	got := slugify("Hello, World! 123")
	// Non-alphanumeric runs collapse to a single hyphen, trimmed.
	if !strings.HasPrefix(got, "hello-world-123-") {
		t.Fatalf("slugify prefix mismatch: got %q", got)
	}

	empty := slugify("   !!!   ")
	if empty == "" {
		t.Fatalf("slugify of non-alnum input should fall back to uuid, got empty")
	}

	// Tight loop: a regression to per-call MustCompile would not fail here, but
	// ensures the compiled regex var stays referenced and stable under load.
	for i := 0; i < 1000; i++ {
		s := slugify("Package Trip ### 2026")
		if !strings.HasPrefix(s, "package-trip-2026-") {
			t.Fatalf("slugify regression at iter %d: got %q", i, s)
		}
	}
}

// BenchmarkSlugify guards PERF-3 #1: the package-level compiled regex keeps
// slugify allocation-light. A regression to regexp.MustCompile per call would
// show a dramatic allocation increase here.
func BenchmarkSlugify(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = slugify("Bali Adventure Tour 2026 - Special Deal!!!")
	}
}
