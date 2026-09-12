package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The bug this guards: the OAuth token was sent as x-api-key, which the API
// answers with a flat 401. It must go out as a bearer with the oauth beta header.
func TestFetchUsageSendsOAuthBearer(t *testing.T) {
	var gotAuth, gotBeta, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotAPIKey = r.Header.Get("x-api-key")
		h := w.Header()
		h.Set("anthropic-ratelimit-unified-status", "allowed")
		h.Set("anthropic-ratelimit-unified-5h-utilization", "0.43")
		h.Set("anthropic-ratelimit-unified-5h-reset", "1789258200")
		h.Set("anthropic-ratelimit-unified-7d-utilization", "0.6")
		h.Set("anthropic-ratelimit-unified-representative-claim", "five_hour")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	creds := `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-test","subscriptionType":"max"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	info, err := fetchUsage()
	if err != nil {
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
	if info.FiveHourUtilization != 0.43 || info.SevenDayUtilization != 0.6 {
		t.Errorf("utilization = %v / %v, want 0.43 / 0.6", info.FiveHourUtilization, info.SevenDayUtilization)
	}
	if info.FiveHourReset != 1789258200 || info.RepresentativeClaim != "five_hour" {
		t.Errorf("reset = %d, claim = %q", info.FiveHourReset, info.RepresentativeClaim)
	}
}

// A refreshed token on disk must be picked up without a restart.
func TestFetchUsageRereadsCredentials(t *testing.T) {
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	path := filepath.Join(home, ".claude", ".credentials.json")
	write := func(tok string) {
		if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"`+tok+`"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	write("first")
	if _, err := fetchUsage(); err != nil {
		t.Fatal(err)
	}
	write("second")
	if _, err := fetchUsage(); err != nil {
		t.Fatal(err)
	}
	if last != "Bearer second" {
		t.Errorf("Authorization = %q, want the refreshed token", last)
	}
}

// A non-200 must carry the API's reason, not just the status code.
func TestFetchUsageErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"OAuth authentication is currently not supported."}}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600)

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	_, err := fetchUsage()
	if err == nil {
		t.Fatal("want an error")
	}
	if want := "OAuth authentication is currently not supported."; !contains(err.Error(), want) {
		t.Errorf("error = %q, want it to quote the API message", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
