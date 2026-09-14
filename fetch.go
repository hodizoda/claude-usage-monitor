package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Window is one rate-limit window: how much of it is spent, and when it resets.
type Window struct {
	Utilization float64 `json:"utilization"` // 0..1
	Reset       int64   `json:"reset"`       // unix seconds, 0 when unknown
}

// ScopedLimit is a weekly limit that applies to one model only — the
// "Current week (Fable)" row in the /usage panel.
type ScopedLimit struct {
	Label       string  `json:"label"`
	Utilization float64 `json:"utilization"` // 0..1
	Reset       int64   `json:"reset"`
	Severity    string  `json:"severity"` // normal | warning | critical
	Active      bool    `json:"active"`
}

// BreakdownRow is one surface's share of the weekly window.
type BreakdownRow struct {
	Label   string  `json:"label"`
	Percent float64 `json:"percent"`
}

type RateLimitInfo struct {
	Timestamp time.Time `json:"timestamp"`

	FiveHour Window `json:"five_hour"`
	SevenDay Window `json:"seven_day"`

	ScopedWeekly []ScopedLimit  `json:"scoped_weekly,omitempty"`
	Breakdown    []BreakdownRow `json:"seven_day_breakdown,omitempty"`

	ExtraUsageEnabled bool `json:"extra_usage_enabled"`
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

// apiBase is the API root. A variable so tests can point it at httptest.
var apiBase = "https://api.anthropic.com"

// The wire shape of GET /api/oauth/usage — the endpoint behind the CLI's
// /usage panel. Percentages arrive on a 0..100 scale.
type usageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type usageLimit struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	Severity string  `json:"severity"`
	ResetsAt string  `json:"resets_at"`
	IsActive bool    `json:"is_active"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type usagePayload struct {
	FiveHour *usageWindow `json:"five_hour"`
	SevenDay *usageWindow `json:"seven_day"`
	Limits   []usageLimit `json:"limits"`

	ExtraUsage struct {
		IsEnabled bool `json:"is_enabled"`
	} `json:"extra_usage"`

	SevenDayBreakdown *struct {
		Rows []struct {
			DisplayName string  `json:"display_name"`
			Percent     float64 `json:"percent"`
		} `json:"rows"`
	} `json:"seven_day_breakdown"`
}

// authRequest builds a request carrying the subscription token. Claude Code
// tokens are OAuth bearers, not API keys: sent as x-api-key they get a flat 401.
// Credentials are re-read per call because Claude Code rotates the token in
// place and a long-running TUI outlives one.
func authRequest(method, url string, body io.Reader) (*http.Request, error) {
	creds, err := loadCredentials()
	if err != nil {
		return nil, err
	}
	token := creds.ClaudeAiOauth.AccessToken
	if token == "" {
		return nil, fmt.Errorf("no access token in credentials")
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

// Profile is the account's plan, read from the API rather than from the
// credentials file: that file records the tier as it was at the last login and
// goes stale the moment the plan changes.
type Profile struct {
	Subscription string // "max"
	Tier         string // "default_claude_max_20x"
}

func fetchProfile() (*Profile, error) {
	req, err := authRequest("GET", apiBase+"/api/oauth/profile", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("profile call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("profile returned %d: %s", resp.StatusCode, readSnippet(resp.Body))
	}

	// The endpoint also carries name and email. Only the plan is decoded —
	// nothing else belongs in this tool's memory.
	var body struct {
		Organization struct {
			OrganizationType string `json:"organization_type"`
			RateLimitTier    string `json:"rate_limit_tier"`
		} `json:"organization"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return &Profile{
		Subscription: strings.TrimPrefix(body.Organization.OrganizationType, "claude_"),
		Tier:         body.Organization.RateLimitTier,
	}, nil
}

// planLabel renders the footer's plan, e.g. "max (20x)".
func planLabel(subscription, tier string) string {
	if subscription == "" {
		subscription = "unknown"
	}
	if tier == "" {
		return subscription
	}
	// "default_claude_max_20x" -> "20x"
	if i := strings.LastIndex(tier, "_"); i >= 0 {
		tier = tier[i+1:]
	}
	if tier == "" || tier == subscription {
		return subscription
	}
	return subscription + " (" + tier + ")"
}

// fetchUsage reads the usage panel's own endpoint. It is a plain GET: no
// inference call, so it costs nothing and does not inflate the numbers it
// reports.
func fetchUsage() (*RateLimitInfo, error) {
	req, err := authRequest("GET", apiBase+"/api/oauth/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("usage returned %d: %s", resp.StatusCode, readSnippet(resp.Body))
	}

	var p usagePayload
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("parse usage: %w", err)
	}
	return buildInfo(&p), nil
}

func buildInfo(p *usagePayload) *RateLimitInfo {
	info := &RateLimitInfo{
		Timestamp:         time.Now(),
		FiveHour:          window(p.FiveHour),
		SevenDay:          window(p.SevenDay),
		ExtraUsageEnabled: p.ExtraUsage.IsEnabled,
	}
	for _, l := range p.Limits {
		// weekly_scoped is the per-model row; every other kind duplicates a
		// window we already read from the top-level fields.
		if l.Kind != "weekly_scoped" || l.Scope == nil || l.Scope.Model == nil {
			continue
		}
		info.ScopedWeekly = append(info.ScopedWeekly, ScopedLimit{
			Label:       l.Scope.Model.DisplayName,
			Utilization: l.Percent / 100,
			Reset:       parseRFC3339(l.ResetsAt),
			Severity:    l.Severity,
			Active:      l.IsActive,
		})
	}
	if p.SevenDayBreakdown != nil {
		for _, r := range p.SevenDayBreakdown.Rows {
			info.Breakdown = append(info.Breakdown, BreakdownRow{Label: r.DisplayName, Percent: r.Percent})
		}
	}
	return info
}

func window(w *usageWindow) Window {
	if w == nil {
		return Window{}
	}
	return Window{Utilization: w.Utilization / 100, Reset: parseRFC3339(w.ResetsAt)}
}

func parseRFC3339(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// readSnippet quotes an error body without letting a large one reach the terminal.
func readSnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 400))
	for i, c := range b {
		if c < 0x20 && c != '\n' && c != '\t' {
			b[i] = ' '
		}
	}
	return string(b)
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
	return time.Unix(unix, 0).Local().Format("Mon at 3:04 PM")
}
