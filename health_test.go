package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// servePage points the status-page client at a stub returning this indicator.
func servePage(t *testing.T, indicator, description string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":{"indicator":"` + indicator + `","description":"` + description + `"}}`))
	}))
	t.Cleanup(srv.Close)
	old := statusBase
	statusBase = srv.URL
	t.Cleanup(func() { statusBase = old })
}

func TestProbeHealthClassifiesResponses(t *testing.T) {
	cases := []struct {
		name string
		code int
		want HealthKind
	}{
		{"answers", 200, HealthOK},
		// Being capped is this tool's normal state near a limit. Reporting it
		// as an outage is the failure mode that matters here.
		{"rate limited", 429, HealthLimited},
		{"bad token", 401, HealthAuth},
		{"forbidden", 403, HealthAuth},
		{"server error", 500, HealthDown},
		{"gateway", 503, HealthDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeCreds(t, "t")
			servePage(t, "none", "All Systems Operational")
			serveUsage(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.code)
				w.Write([]byte(`{"error":{"message":"x"}}`))
			})

			if got := probeHealth().Kind; got != c.want {
				t.Errorf("HTTP %d classified as %q, want %q", c.code, got, c.want)
			}
		})
	}
}

func TestProbeHealthUnreachableAPI(t *testing.T) {
	writeCreds(t, "t")
	servePage(t, "none", "All Systems Operational")

	// A server that is closed before use: the connection is refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	old := apiBase
	apiBase = url
	t.Cleanup(func() { apiBase = old })

	h := probeHealth()
	if h.Kind != HealthDown {
		t.Fatalf("kind = %q, want down", h.Kind)
	}
	// The status page says the service is fine, so the fault is local — the
	// summary has to say so rather than blame Anthropic.
	if !strings.Contains(h.Summary(), "no incident") {
		t.Errorf("summary = %q, want it to point at the local side", h.Summary())
	}
}

func TestSummaryCarriesOngoingIncident(t *testing.T) {
	writeCreds(t, "t")
	servePage(t, "minor", "Partially Degraded Service")
	serveUsage(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	h := probeHealth()
	if h.Kind != HealthOK {
		t.Fatalf("kind = %q, want ok", h.Kind)
	}
	if !strings.Contains(h.Summary(), "Partially Degraded Service") {
		t.Errorf("summary = %q, want the incident text even when the ping worked", h.Summary())
	}
}

// The ping must be the cheapest possible call: one Haiku token.
func TestProbeHealthUsesMinimalRequest(t *testing.T) {
	var body, path string
	writeCreds(t, "t")
	servePage(t, "none", "All Systems Operational")
	serveUsage(t, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		body, path = string(b[:n]), r.URL.Path
		w.WriteHeader(200)
	})

	probeHealth()
	if path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", path)
	}
	if !strings.Contains(body, `"max_tokens":1`) {
		t.Errorf("body = %q, want max_tokens 1", body)
	}
	if !strings.Contains(body, "haiku") {
		t.Errorf("body = %q, want the haiku model", body)
	}
}
