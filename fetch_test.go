package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A trimmed copy of a real GET /api/oauth/usage response. Percentages arrive
// on a 0..100 scale; everything downstream works in fractions.
const sampleUsage = `{
  "five_hour": {"utilization": 1.0, "resets_at": "2026-09-14T18:00:00.431656+00:00"},
  "seven_day": {"utilization": 83.0, "resets_at": "2026-09-16T08:00:00.431676+00:00"},
  "seven_day_opus": null,
  "extra_usage": {"is_enabled": false},
  "limits": [
    {"kind": "session", "percent": 1, "severity": "normal",
     "resets_at": "2026-09-14T18:00:00.431656+00:00", "scope": null, "is_active": false},
    {"kind": "weekly_all", "percent": 83, "severity": "warning",
     "resets_at": "2026-09-16T08:00:00.431676+00:00", "scope": null, "is_active": false},
    {"kind": "weekly_scoped", "percent": 100, "severity": "critical",
     "resets_at": "2026-09-16T08:00:00.431862+00:00",
     "scope": {"model": {"id": null, "display_name": "Fable"}}, "is_active": true}
  ],
  "seven_day_breakdown": {"rows": [
    {"key": "claude_code", "display_name": "Claude Code", "percent": 96},
    {"key": "chat", "display_name": "Chats", "percent": 1}
  ]}
}`

// writeCreds points HOME at a temp dir holding a credentials file.
func writeCreds(t *testing.T, token string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"claudeAiOauth":{"accessToken":"` + token + `","subscriptionType":"max"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func serveUsage(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
}

// The bug this guards: the OAuth token went out as x-api-key, which the API
// answers with a flat 401.
func TestFetchUsageSendsOAuthBearer(t *testing.T) {
	var gotAuth, gotBeta, gotAPIKey, gotMethod, gotPath string
	writeCreds(t, "sk-ant-oat01-test")
	serveUsage(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
		gotAPIKey, gotMethod, gotPath = r.Header.Get("x-api-key"), r.Method, r.URL.Path
		w.Write([]byte(sampleUsage))
	})

	if _, err := fetchUsage(); err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	if gotAuth != "Bearer sk-ant-oat01-test" {
		t.Errorf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q, want oauth-2025-04-20", gotBeta)
	}
	if gotAPIKey != "" {
		t.Errorf("x-api-key = %q, want it unset", gotAPIKey)
	}
	// A GET on the usage endpoint costs nothing. A POST to /v1/messages would
	// bill the user for the number it is reporting.
	if gotMethod != "GET" || gotPath != "/api/oauth/usage" {
		t.Errorf("request = %s %s, want GET /api/oauth/usage", gotMethod, gotPath)
	}
}

func TestFetchUsageParsesWindowsAndScopedLimits(t *testing.T) {
	writeCreds(t, "t")
	serveUsage(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sampleUsage)) })

	info, err := fetchUsage()
	if err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	// 0..100 on the wire must become 0..1, or every bar renders full.
	if info.FiveHour.Utilization != 0.01 {
		t.Errorf("five hour = %v, want 0.01", info.FiveHour.Utilization)
	}
	if info.SevenDay.Utilization != 0.83 {
		t.Errorf("seven day = %v, want 0.83", info.SevenDay.Utilization)
	}
	if info.FiveHour.Reset != 1789408800 {
		t.Errorf("five hour reset = %d, want 1789408800", info.FiveHour.Reset)
	}
	// Only weekly_scoped rows become per-model rows; session and weekly_all
	// duplicate the windows above.
	if len(info.ScopedWeekly) != 1 {
		t.Fatalf("scoped rows = %d, want 1: %+v", len(info.ScopedWeekly), info.ScopedWeekly)
	}
	sl := info.ScopedWeekly[0]
	if sl.Label != "Fable" || sl.Utilization != 1 || sl.Severity != "critical" || !sl.Active {
		t.Errorf("scoped = %+v, want Fable 1.0 critical active", sl)
	}
	if len(info.Breakdown) != 2 || info.Breakdown[0].Label != "Claude Code" || info.Breakdown[0].Percent != 96 {
		t.Errorf("breakdown = %+v", info.Breakdown)
	}
	if info.ExtraUsageEnabled {
		t.Error("extra usage should be disabled in the sample")
	}
}

// A refreshed token on disk must be picked up without a restart.
func TestFetchUsageRereadsCredentials(t *testing.T) {
	var last string
	home := writeCreds(t, "first")
	serveUsage(t, func(w http.ResponseWriter, r *http.Request) {
		last = r.Header.Get("Authorization")
		w.Write([]byte(sampleUsage))
	})

	if _, err := fetchUsage(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"second"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchUsage(); err != nil {
		t.Fatal(err)
	}
	if last != "Bearer second" {
		t.Errorf("Authorization = %q, want the refreshed token", last)
	}
}

// A non-200 must carry the API's reason, not just the status code.
func TestFetchUsageErrorIncludesBody(t *testing.T) {
	writeCreds(t, "t")
	serveUsage(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"OAuth authentication is currently not supported."}}`))
	})

	_, err := fetchUsage()
	if err == nil {
		t.Fatal("want an error")
	}
	if want := "OAuth authentication is currently not supported."; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to quote the API message", err)
	}
}
