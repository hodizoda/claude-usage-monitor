package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func main() {
	once := flag.Bool("once", false, "Print usage once and exit (no TUI)")
	jsonOut := flag.Bool("json", false, "Print usage as JSON and exit (implies --once)")
	preview := flag.Bool("preview", false, "Render one frame of the TUI to stdout and exit")
	// The usage endpoint's request budget is small and shared with Claude Code's
	// own polling: 30-50s refreshes still drew frequent 429s. Each refresh picks
	// one of these at random; press r for a fresh number in between.
	intervalFlag := flag.String("interval", "3m,5m,8m,13m", "Refresh delay for TUI mode, or a comma list to pick from at random")
	// The usage read is a free GET; the health ping is a real inference call
	// that lands in the 5-hour window, so it gets its own, slower clock.
	pingInterval := flag.Duration("ping-interval", 30*time.Minute, "How often to ping Haiku to check the API is up (0 disables)")
	ping := flag.Bool("ping", false, "Include one health ping in --once / --json output")
	flag.Parse()

	intervals, err := parseIntervals(*intervalFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--interval: %v\n", err)
		os.Exit(2)
	}

	creds, err := loadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading credentials: %v\n", err)
		os.Exit(1)
	}
	if creds.ClaudeAiOauth.AccessToken == "" {
		fmt.Fprintln(os.Stderr, "No access token found in credentials")
		os.Exit(1)
	}

	// The credentials file's tier is a snapshot from the last login and goes
	// stale on a plan change; the profile endpoint is authoritative and free.
	subscription, tier := creds.ClaudeAiOauth.SubscriptionType, creds.ClaudeAiOauth.RateLimitTier
	if prof, err := fetchProfile(); err == nil {
		subscription, tier = prof.Subscription, prof.Tier
	}

	if *preview {
		// Force truecolor so the preview shows the two-tone bar even when
		// stdout isn't a TTY.
		lipgloss.SetColorProfile(termenv.TrueColor)
		info, err := fetchUsage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		m := newModel(subscription, tier, intervals, *pingInterval)
		m.info = info
		m.fetching = false
		m.lastFetch = time.Now()
		m.nextFetchAt = time.Now().Add(pickInterval(intervals))
		fmt.Println(m.View())
		return
	}

	if *jsonOut || *once {
		info, err := fetchUsage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		var health *Health
		if *ping {
			h := probeHealth()
			health = &h
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(struct {
				*RateLimitInfo
				Health *Health `json:"health,omitempty"`
			}{info, health})
		} else {
			printPlain(info, health, subscription, tier)
		}
		return
	}

	if err := runTUI(subscription, tier, intervals, *pingInterval); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func printPlain(info *RateLimitInfo, health *Health, sub, tier string) {
	fmt.Printf("Plan: %s\n\n", planLabel(sub, tier))

	plainBar := func(pct float64, w int) string {
		filled := int(math.Round(pct * float64(w)))
		if filled > w {
			filled = w
		}
		if filled < 0 {
			filled = 0
		}
		return strings.Repeat("█", filled) + strings.Repeat("░", w-filled)
	}

	line := func(label string, u float64, reset string) {
		fmt.Printf("%-22s [%s] %5.1f%%\n  Resets %s\n\n", label, plainBar(u, 30), u*100, reset)
	}

	line("Current session (5h)", info.FiveHour.Utilization, "in "+formatResetRelative(info.FiveHour.Reset))
	line("Weekly (7d)", info.SevenDay.Utilization, formatResetAbsolute(info.SevenDay.Reset))
	for _, sl := range info.ScopedWeekly {
		line("Weekly ("+sl.Label+")", sl.Utilization, formatResetAbsolute(sl.Reset))
	}

	if len(info.Breakdown) > 0 {
		parts := make([]string, 0, len(info.Breakdown))
		for _, b := range info.Breakdown {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", b.Label, b.Percent))
		}
		fmt.Printf("Week so far: %s\n", strings.Join(parts, " · "))
	}
	if health != nil {
		fmt.Printf("Health: %s\n", health.Summary())
		if health.Kind == HealthLimited {
			fmt.Printf("\n%s\n%s\n", restingCat, restMessage(info))
		}
	}
}
