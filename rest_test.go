package main

import (
	"errors"
	"regexp"
	"slices"
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
	m := newModel("max", "default_claude_max_5x", testIntervals, 30*time.Minute)
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

var testIntervals = []time.Duration{3 * time.Minute, 5 * time.Minute, 8 * time.Minute, 13 * time.Minute}

func TestNextBackoffSpacesOutRetries(t *testing.T) {
	interval := 5 * time.Minute
	plain := errors.New("boom")

	// First failure waits a normal interval, then doubles, then stops at the
	// ceiling — answering a rate limit at the same rate is more rate limiting.
	first := nextBackoff(0, interval, plain)
	if first != interval {
		t.Errorf("first backoff = %v, want %v", first, interval)
	}
	if got := nextBackoff(first, interval, plain); got != 10*time.Minute {
		t.Errorf("second backoff = %v, want 10m", got)
	}
	if got := nextBackoff(20*time.Minute, interval, plain); got != 30*time.Minute {
		t.Errorf("capped backoff = %v, want the 30m ceiling", got)
	}

	// The ceiling must sit above every normal interval, or an error would
	// make the loop poll faster than it does when healthy.
	for _, d := range testIntervals {
		if got := nextBackoff(d, d, plain); got < d {
			t.Errorf("backoff after a %v interval = %v, shorter than normal", d, got)
		}
	}

	// Retry-After may lengthen the wait...
	longer := &APIError{Status: 429, RetryAfter: 20 * time.Minute}
	if got := nextBackoff(0, interval, longer); got != 20*time.Minute {
		t.Errorf("backoff = %v, want the server's 20m", got)
	}
	// ...but never shorten it below what the client would wait anyway.
	shorter := &APIError{Status: 429, RetryAfter: 45 * time.Second}
	if got := nextBackoff(0, interval, shorter); got != interval {
		t.Errorf("backoff = %v, want %v despite a 45s Retry-After", got, interval)
	}
	// ...and never past the ceiling.
	huge := &APIError{Status: 429, RetryAfter: 2 * time.Hour}
	if got := nextBackoff(0, interval, huge); got != 30*time.Minute {
		t.Errorf("backoff = %v, want it clamped to 30m", got)
	}
}

// The card is a fixed-width box: a multi-line JSON body must never reach it.
func TestViewKeepsRateLimitErrorInsideTheCard(t *testing.T) {
	m := newModel("max", "default_claude_max_20x", testIntervals, 30*time.Minute)
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
	m := newModel("max", "default_claude_max_20x", testIntervals, 30*time.Minute)
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

// Every refresh must come from the configured set, and all of the set must be
// reachable — a draw stuck on one value is a fixed period again.
func TestPickIntervalDrawsFromTheWholeSet(t *testing.T) {
	seen := map[time.Duration]int{}
	for i := 0; i < 2000; i++ {
		d := pickInterval(testIntervals)
		seen[d]++
	}
	for d := range seen {
		if !slices.Contains(testIntervals, d) {
			t.Fatalf("drew %v, which is not in the set", d)
		}
	}
	if len(seen) != len(testIntervals) {
		t.Errorf("drew %d of %d intervals in 2000 picks: %v", len(seen), len(testIntervals), seen)
	}

	if got := pickInterval([]time.Duration{time.Minute}); got != time.Minute {
		t.Errorf("single interval = %v, want a fixed 1m", got)
	}
}

func TestParseIntervals(t *testing.T) {
	got, err := parseIntervals("3m, 5m,8m,13m")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, testIntervals) {
		t.Errorf("parsed %v, want %v", got, testIntervals)
	}
	if got, err := parseIntervals("90s"); err != nil || !slices.Equal(got, []time.Duration{90 * time.Second}) {
		t.Errorf("single value = %v, %v", got, err)
	}
	// Bad input fails loudly rather than falling back to something unasked for.
	for _, bad := range []string{"", ",", "3x", "5s", "3m,-1m"} {
		if _, err := parseIntervals(bad); err == nil {
			t.Errorf("parseIntervals(%q) accepted bad input", bad)
		}
	}
}
