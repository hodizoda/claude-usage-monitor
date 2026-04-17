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
	interval := flag.Duration("interval", 30*time.Second, "Refresh interval for TUI mode")
	flag.Parse()

	creds, err := loadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading credentials: %v\n", err)
		os.Exit(1)
	}
	apiKey := creds.ClaudeAiOauth.AccessToken
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "No access token found in credentials")
		os.Exit(1)
	}

	if *preview {
		// Force truecolor so the preview shows the two-tone bar even when
		// stdout isn't a TTY.
		lipgloss.SetColorProfile(termenv.TrueColor)
		info, err := fetchUsage(apiKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		m := newModel(apiKey,
			creds.ClaudeAiOauth.SubscriptionType,
			creds.ClaudeAiOauth.RateLimitTier,
			*interval)
		m.info = info
		m.fetching = false
		m.lastFetch = time.Now()
		m.nextFetchAt = time.Now().Add(*interval)
		fmt.Println(m.View())
		return
	}

	if *jsonOut || *once {
		info, err := fetchUsage(apiKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(info)
		} else {
			printPlain(info, creds.ClaudeAiOauth.SubscriptionType, creds.ClaudeAiOauth.RateLimitTier)
		}
		return
	}

	if err := runTUI(apiKey,
		creds.ClaudeAiOauth.SubscriptionType,
		creds.ClaudeAiOauth.RateLimitTier,
		*interval); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func printPlain(info *RateLimitInfo, sub, tier string) {
	fmt.Printf("Subscription: %s  Tier: %s\n\n", sub, tier)

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

	fmt.Printf("Current session (5h)   [%s] %5.1f%%\n",
		plainBar(info.FiveHourUtilization, 30), info.FiveHourUtilization*100)
	fmt.Printf("  Resets in %s\n\n", formatResetRelative(info.FiveHourReset))

	fmt.Printf("Weekly (7d)            [%s] %5.1f%%\n",
		plainBar(info.SevenDayUtilization, 30), info.SevenDayUtilization*100)
	fmt.Printf("  Resets %s\n\n", formatResetAbsolute(info.SevenDayReset))

	fmt.Printf("Status: %s · binding: %s · overage: %s\n",
		info.Status, info.RepresentativeClaim, info.OverageStatus)
}
