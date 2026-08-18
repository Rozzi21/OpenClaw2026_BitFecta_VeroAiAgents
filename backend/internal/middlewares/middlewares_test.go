package middlewares

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestIPRateLimiterEvictsByLastUseWithoutConsumingToken(t *testing.T) {
	l := &ipRateLimiter{every: rate.Every(time.Hour), burst: 2, maxEntries: 10}
	limiter := l.get("192.0.2.1")
	if !limiter.Allow() {
		t.Fatal("first token should be available")
	}

	l.evictIdle(time.Now(), time.Minute)
	if got := l.entries.Load(); got != 1 {
		t.Fatalf("active limiter evicted: entries=%d", got)
	}
	if !limiter.Allow() {
		t.Fatal("janitor consumed a rate-limit token")
	}

	l.evictIdle(time.Now().Add(2*time.Minute), time.Minute)
	if got := l.entries.Load(); got != 0 {
		t.Fatalf("idle limiter not evicted: entries=%d", got)
	}
}

func TestIPRateLimiterCapUsesConstantTimeCounter(t *testing.T) {
	l := &ipRateLimiter{every: rate.Every(time.Second), burst: 1, maxEntries: 2}
	l.get("192.0.2.1")
	l.get("192.0.2.2")
	l.get("192.0.2.3")

	if got := l.entries.Load(); got != 2 {
		t.Fatalf("entry cap exceeded: entries=%d", got)
	}
	if _, tracked := l.limiters.Load("192.0.2.3"); tracked {
		t.Fatal("IP beyond cap should receive an untracked limiter")
	}
}
