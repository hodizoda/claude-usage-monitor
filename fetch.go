package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// RateLimitInfo is parsed from the Anthropic API's response headers.
type RateLimitInfo struct {
	Timestamp time.Time `json:"timestamp"`

	Status string `json:"status"`

	FiveHourStatus      string  `json:"five_hour_status"`
	FiveHourReset       int64   `json:"five_hour_reset"`
	FiveHourUtilization float64 `json:"five_hour_utilization"`

	SevenDayStatus      string  `json:"seven_day_status"`
	SevenDayReset       int64   `json:"seven_day_reset"`
	SevenDayUtilization float64 `json:"seven_day_utilization"`

	RepresentativeClaim   string  `json:"representative_claim"`
	FallbackPercentage    float64 `json:"fallback_percentage"`
	OverageStatus         string  `json:"overage_status"`
	OverageDisabledReason string  `json:"overage_disabled_reason"`
	OverageReset          int64   `json:"overage_reset,omitempty"`
	UpgradePaths          string  `json:"upgrade_paths,omitempty"`
}

type Credentials struct {
	ClaudeAiOauth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

func loadCredentials() (*Credentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return &creds, nil
}

func fetchUsage(apiKey string) (*RateLimitInfo, error) {
	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("api returned %d", resp.StatusCode)
	}

	h := resp.Header
	return &RateLimitInfo{
		Timestamp:             time.Now(),
		Status:                h.Get("anthropic-ratelimit-unified-status"),
		FiveHourStatus:        h.Get("anthropic-ratelimit-unified-5h-status"),
		FiveHourReset:         parseUnix(h.Get("anthropic-ratelimit-unified-5h-reset")),
		FiveHourUtilization:   parseFloat(h.Get("anthropic-ratelimit-unified-5h-utilization")),
		SevenDayStatus:        h.Get("anthropic-ratelimit-unified-7d-status"),
		SevenDayReset:         parseUnix(h.Get("anthropic-ratelimit-unified-7d-reset")),
		SevenDayUtilization:   parseFloat(h.Get("anthropic-ratelimit-unified-7d-utilization")),
		RepresentativeClaim:   h.Get("anthropic-ratelimit-unified-representative-claim"),
		FallbackPercentage:    parseFloat(h.Get("anthropic-ratelimit-unified-fallback-percentage")),
		OverageStatus:         h.Get("anthropic-ratelimit-unified-overage-status"),
		OverageDisabledReason: h.Get("anthropic-ratelimit-unified-overage-disabled-reason"),
		OverageReset:          parseUnix(h.Get("anthropic-ratelimit-unified-overage-reset")),
		UpgradePaths:          h.Get("anthropic-ratelimit-unified-upgrade-paths"),
	}, nil
}

func parseUnix(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// formatResetRelative returns e.g. "3 hr 45 min" or "6 days".
func formatResetRelative(unix int64) string {
	if unix == 0 {
		return "—"
	}
	dur := time.Until(time.Unix(unix, 0))
	if dur < 0 {
		return "now"
	}
	if dur < time.Minute {
		return fmt.Sprintf("%d sec", int(dur.Seconds()))
	}
	if dur < time.Hour {
		return fmt.Sprintf("%d min", int(dur.Minutes()))
	}
	if dur < 24*time.Hour {
		h := int(dur.Hours())
		m := int(dur.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%d hr", h)
		}
		return fmt.Sprintf("%d hr %d min", h, m)
	}
	d := int(dur.Hours()) / 24
	h := int(dur.Hours()) % 24
	if h == 0 {
		return fmt.Sprintf("%d days", d)
	}
	return fmt.Sprintf("%dd %dh", d, h)
}

// formatResetAbsolute returns e.g. "Wed at 9:25 PM".
func formatResetAbsolute(unix int64) string {
	if unix == 0 {
		return "—"
	}
	t := time.Unix(unix, 0).Local()
	return t.Format("Mon at 3:04 PM")
}
