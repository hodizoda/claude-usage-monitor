package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestCooldownPicksTheBlockingLimit(t *testing.T) {
	soon := time.Now().Add(2 * time.Hour).Unix()
	later := time.Now().Add(40 * time.Hour).Unix()

	// The per-model weekly limit is the full one; the 5-hour window is idle.
	// Waiting on the 5-hour clock would be wrong advice.
	info := &RateLimitInfo{
		FiveHour:     Window{Utilization: 0.02, Reset: soon},
		SevenDay:     Window{Utilization: 0.83, Reset: later},
		ScopedWeekly: []ScopedLimit{{Label: "Fable", Utilization: 1, Reset: later, Severity: "critical", Active: true}},
	}
	label, reset := cooldown(info)
	if label != "Fable weekly limit" || reset != later {
		t.Errorf("cooldown = %q @ %d, want the Fable weekly limit @ %d", label, reset, later)
	}

	// When both are full, the one that lifts first is the one to wait for.
	info.FiveHour.Utilization = 1
	if label, reset := cooldown(info); label != "5-hour window" || reset != soon {
		t.Errorf("cooldown = %q @ %d, want the 5-hour window @ %d", label, reset, soon)
	}

	// Rate limited with nothing reported full: fall back to the 5-hour clock
	// rather than claiming no cool down exists.
	empty := &RateLimitInfo{FiveHour: Window{Utilization: 0.4, Reset: soon}}
	if label, reset := cooldown(empty); label != "5-hour window" || reset != soon {
		t.Errorf("fallback = %q @ %d, want the 5-hour window @ %d", label, reset, soon)
	}
}

func TestRestMessageCountsDown(t *testing.T) {
	info := &RateLimitInfo{FiveHour: Window{Utilization: 1, Reset: time.Now().Add(90 * time.Minute).Unix()}}
	msg := restMessage(info)
	if !strings.Contains(msg, "1 hr 29 min") && !strings.Contains(msg, "1 hr 30 min") {
		t.Errorf("message = %q, want a countdown to the reset", msg)
	}

	past := &RateLimitInfo{FiveHour: Window{Utilization: 1, Reset: time.Now().Add(-time.Minute).Unix()}}
	if got := restMessage(past); !strings.Contains(got, "over") {
		t.Errorf("message = %q, want it to say the cool down is over", got)
	}
}

// The card has to actually reach the screen: a limited ping must render the
// cat and the countdown, not the ordinary health line.
func TestViewShowsCoolDownCardWhenLimited(t *testing.T) {
	m := newModel("max", "default_claude_max_5x", 30*time.Second, 20*time.Second, 30*time.Minute)
	m.fetching = false
	m.info = &RateLimitInfo{
		FiveHour: Window{Utilization: 1, Reset: time.Now().Add(90 * time.Minute).Unix()},
		SevenDay: Window{Utilization: 0.83, Reset: time.Now().Add(40 * time.Hour).Unix()},
	}
	limited := Health{Kind: HealthLimited, At: time.Now()}
	m.health = &limited

	out := stripANSI(m.View())
	if !strings.Contains(out, "Cool down") {
		t.Errorf("view has no cool-down line:\n%s", out)
	}
	if !strings.Contains(out, "'---''(_/--'") {
		t.Errorf("view has no resting cat:\n%s", out)
	}

	// Healthy ping and room in every window: no card. The window has to be
	// drained too, since a spent window now raises the card on its own.
	ok := Health{Kind: HealthOK, At: time.Now(), Latency: 600 * time.Millisecond}
	m.health = &ok
	m.info.FiveHour.Utilization = 0.2
	if out := stripANSI(m.View()); strings.Contains(out, "Cool down") {
		t.Errorf("cool-down card shown while healthy and under the limits:\n%s", out)
	}
}

// The gradient must colour the art without altering it: same glyphs, more
// than one colour, and spaces left alone.
func TestRenderPetPaintsAGradient(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)

	out := renderPet(restingCat)
	if stripANSI(out) != restingCat {
		t.Errorf("art changed:\n%q\nwant:\n%q", stripANSI(out), restingCat)
	}
	colors := map[string]bool{}
	for _, m := range ansiColor.FindAllStringSubmatch(out, -1) {
		colors[m[1]] = true
	}
	if len(colors) < 8 {
		t.Errorf("%d distinct colours, want a real ramp", len(colors))
	}
}

var (
	ansiRE    = regexp.MustCompile("\x1b\\[[0-9;]*m")
	ansiColor = regexp.MustCompile("\x1b\\[38;2;([0-9;]+)m")
)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestNextBackoffSpacesOutRetries(t *testing.T) {
	interval := 30 * time.Second
	plain := errors.New("boom")

	// First failure waits the normal interval, then doubles, then stops at the
	// ceiling — answering a rate limit at the same rate is more rate limiting.
	first := nextBackoff(0, interval, plain)
	if first != interval {
		t.Errorf("first backoff = %v, want %v", first, interval)
	}
	if got := nextBackoff(first, interval, plain); got != time.Minute {
		t.Errorf("second backoff = %v, want 1m", got)
	}
	if got := nextBackoff(4*time.Minute, interval, plain); got != 5*time.Minute {
		t.Errorf("capped backoff = %v, want the 5m ceiling", got)
	}

	// A server-set Retry-After wins over the doubling.
	limited := &APIError{Status: 429, RetryAfter: 45 * time.Second}
	if got := nextBackoff(4*time.Minute, interval, limited); got != 45*time.Second {
		t.Errorf("backoff = %v, want the server's 45s", got)
	}
	// ...but not past the ceiling.
	long := &APIError{Status: 429, RetryAfter: time.Hour}
	if got := nextBackoff(0, interval, long); got != 5*time.Minute {
		t.Errorf("backoff = %v, want it clamped to 5m", got)
	}
}

// The card is a fixed-width box: a multi-line JSON body must never reach it.
func TestViewKeepsRateLimitErrorInsideTheCard(t *testing.T) {
	m := newModel("max", "default_claude_max_20x", 30*time.Second, 20*time.Second, 30*time.Minute)
	m.fetching = false
	m.err = &APIError{Status: 429, Message: "Rate limited. Please try again later."}
	m.nextFetchAt = time.Now().Add(30 * time.Second)

	for _, line := range strings.Split(stripANSI(m.View()), "\n") {
		if lipgloss.Width(line) > 70 {
			t.Errorf("line escapes the card (%d cols): %q", lipgloss.Width(line), line)
		}
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "Rate limited") || !strings.Contains(out, "Retrying in") {
		t.Errorf("view should say it is rate limited and when it retries:\n%s", out)
	}
}

// The pet has to appear on the data alone. Waiting for a ping to be refused
// means it only shows up after the interruption it is warning about.
func TestViewShowsPetWhenAWindowIsSpent(t *testing.T) {
	m := newModel("max", "default_claude_max_20x", 30*time.Second, 20*time.Second, 30*time.Minute)
	m.fetching = false
	healthy := Health{Kind: HealthOK, At: time.Now()}
	m.health = &healthy

	// Nothing spent: no pet.
	m.info = &RateLimitInfo{
		FiveHour: Window{Utilization: 0.02, Reset: time.Now().Add(3 * time.Hour).Unix()},
		SevenDay: Window{Utilization: 0.83, Reset: time.Now().Add(40 * time.Hour).Unix()},
	}
	if strings.Contains(stripANSI(m.View()), "Cool down") {
		t.Error("pet shown while nothing is spent")
	}

	// A per-model weekly limit at 100% is a real interruption, even though
	// both top-level windows have room and no ping has failed.
	m.info.ScopedWeekly = []ScopedLimit{{
		Label: "Fable", Utilization: 1, Severity: "critical", Active: true,
		Reset: time.Now().Add(40 * time.Hour).Unix(),
	}}
	out := stripANSI(m.View())
	if !strings.Contains(out, "'---''(_/--'") {
		t.Errorf("no pet with a spent per-model window:\n%s", out)
	}
	if !strings.Contains(out, "Fable weekly limit") {
		t.Errorf("cool down should name the Fable limit:\n%s", out)
	}
}

func TestCappedIgnoresEmptyInfo(t *testing.T) {
	if capped(nil) {
		t.Error("nil info must not read as capped")
	}
	if capped(&RateLimitInfo{}) {
		t.Error("an empty payload must not read as capped")
	}
}

// Every refresh must land inside [interval, interval+jitter], and must not
// always land on the same value — a fixed period is what aligns this client
// with everything else polling the endpoint.
func TestNextIntervalStaysInRangeAndVaries(t *testing.T) {
	const (
		base   = 30 * time.Second
		jitter = 20 * time.Second
	)
	seen := map[time.Duration]bool{}
	for i := 0; i < 500; i++ {
		d := nextInterval(base, jitter)
		if d < base || d > base+jitter {
			t.Fatalf("interval %v outside [%v, %v]", d, base, base+jitter)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Errorf("%d distinct delays in 500 draws, want a spread", len(seen))
	}

	// Zero jitter is a fixed period, for anyone who wants one.
	for i := 0; i < 10; i++ {
		if d := nextInterval(base, 0); d != base {
			t.Fatalf("interval %v with no jitter, want %v", d, base)
		}
	}
}
