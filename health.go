package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// statusBase is Anthropic's public status page (status.anthropic.com 301s here).
// A variable so tests can point it at httptest.
var statusBase = "https://status.claude.com"

type HealthKind string

const (
	HealthOK      HealthKind = "ok"      // inference answered
	HealthLimited HealthKind = "limited" // 429 — the account is capped, the API is fine
	HealthAuth    HealthKind = "auth"    // 401/403 — the token, not the service
	HealthDown    HealthKind = "down"    // 5xx, timeout, or no route to the API
)

// PageStatus is the official incident state, which needs no token and no auth.
// It is what separates "Anthropic is down" from "this box cannot reach it".
type PageStatus struct {
	Indicator   string `json:"indicator"`   // none | minor | major | critical
	Description string `json:"description"` // "All Systems Operational"
}

type Health struct {
	Kind    HealthKind    `json:"kind"`
	Detail  string        `json:"detail,omitempty"`
	Latency time.Duration `json:"-"`
	// time.Duration marshals as nanoseconds; the JSON field promises milliseconds.
	LatencyMS int64       `json:"latency_ms"`
	At        time.Time   `json:"checked_at"`
	Page      *PageStatus `json:"status_page,omitempty"`
}

// probeHealth sends the smallest possible inference request and classifies the
// answer. It never returns an error: the classification is the result.
//
// A rate-limited account answers 429. That is the tool's normal working state
// near a limit and must never be reported as an outage.
func probeHealth() Health {
	h := Health{At: time.Now(), Page: fetchStatusPage()}

	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := authRequest("POST", apiBase+"/v1/messages", bytes.NewBufferString(body))
	if err != nil {
		h.Kind, h.Detail = HealthAuth, err.Error()
		return h
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	h.Latency = time.Since(start)
	h.LatencyMS = h.Latency.Milliseconds()
	if err != nil {
		h.Kind, h.Detail = HealthDown, err.Error()
		return h
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 200:
		h.Kind = HealthOK
	case resp.StatusCode == 429:
		h.Kind, h.Detail = HealthLimited, "rate limited"
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		h.Kind, h.Detail = HealthAuth, apiError(resp).Error()
	default:
		h.Kind, h.Detail = HealthDown, fmt.Sprintf("%d: %s", resp.StatusCode, apiError(resp).Error())
	}
	return h
}

func fetchStatusPage() *PageStatus {
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(statusBase + "/api/v2/status.json")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var page struct {
		Status PageStatus `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil
	}
	return &page.Status
}

// Summary is the one line the TUI and --once print.
func (h Health) Summary() string {
	var s string
	switch h.Kind {
	case HealthOK:
		s = fmt.Sprintf("API ok · %dms", h.Latency.Milliseconds())
	case HealthLimited:
		s = "API up · you are rate limited"
	case HealthAuth:
		s = "auth failed · run claude login"
	case HealthDown:
		s = "API unreachable"
		if h.Page != nil && h.Page.Indicator == "none" {
			// The service says it is fine, so the fault is local to this box.
			s += " · status page reports no incident"
		}
	}
	if h.Page != nil && h.Page.Indicator != "none" && h.Page.Indicator != "" {
		s += " · " + h.Page.Description
	}
	return s
}
