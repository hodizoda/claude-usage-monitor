# claude-usage-monitor

A terminal UI that shows your Claude Max subscription's rate-limit state — current 5-hour window, weekly limit, reset countdowns — modeled after the "Plan usage limits" panel in the Claude web UI.

```
╭────────────────────────────────────────────────────────────────╮
│                                                                │
│   Plan usage limits                                            │
│                                                                │
│   Current session                                              │
│   5-hour window                                     18% used   │
│   Resets in 1 hr 31 min                                        │
│   ██████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   │
│                                                                │
│   Weekly limits                                                │
│   All models                                         8% used   │
│   Resets Thu at 7:00 PM                                        │
│   ████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   │
│                                                                │
│   Status: allowed · binding: five_hour · overage: rejected     │
│                                                                │
│   max (5x)                                   ↻ 30s · [r] [q]   │
│                                                                │
╰────────────────────────────────────────────────────────────────╯
```

## How it works

`GET /api/oauth/usage` — the endpoint behind the CLI's own `/usage` panel. It is a
plain authenticated GET: no inference call, nothing billed, and no contribution to
the numbers it reports.

It returns the 5-hour window, the all-models weekly window, any **per-model weekly
limit** ("Current week (Fable)"), a breakdown of where the week went by surface,
and the extra-usage/credits state.

The plan shown in the footer comes from `GET /api/oauth/profile`, not from the
credentials file: that file records the rate-limit tier as it was at the last
login, so an upgraded account keeps reading as its old tier until the next
`claude login`. Only the organization's plan and tier are decoded from that
response. If the call fails, the credentials file is used as a fallback.

An earlier version of this tool read `anthropic-ratelimit-unified-*` response
headers off a 1-token Haiku call instead. Those headers are real and still work,
but they carry no per-model window, and the probe itself costs usage.

## Health check

Usage data says nothing about whether the API is actually answering, so a separate,
much slower ping (`--ping-interval`, default 30 min) sends one minimum-size Haiku
request and classifies the result:

| Result | Meaning |
|--------|---------|
| `ok` | Inference answered, with the round-trip time |
| `limited` | HTTP 429 — the account is capped. **Not** an outage |
| `auth` | 401/403 — the token, not the service |
| `down` | 5xx, timeout, or no route to the API |

`https://status.claude.com/api/v2/status.json` is read alongside it — no auth, no
tokens. An ongoing incident is shown even when the ping succeeded, and if the ping
fails while the status page reports no incident, the tool says so rather than
blaming Anthropic for a local network fault.

## Cool down

When any window is spent — the 5-hour one, the weekly one, or a per-model weekly
limit — the card grows a countdown and a cat that has the right idea. A 429 from
the health ping raises it too, but the usage data alone is enough: waiting for a
refused request means the interruption has already happened.

```
     |\      _,,,---,,_
     /,`.-'`'    -.  ;-;;,_
    |,4-  ) )-,_. ,\ (  `'-'
   '---''(_/--'  `-'\_)
Cool down 1 hr 33 min — 5-hour window resets. Rest.
```

The window named is the one actually blocking: every window at or above 99.5% is a
candidate and the soonest to lift wins, so a full per-model weekly limit is not
reported as a 5-hour wait.

## Install

```bash
go install github.com/hodizoda/claude-usage-monitor@latest
```

Or build from source:

```bash
git clone git@github.com:hodizoda/claude-usage-monitor.git
cd claude-usage-monitor
go build -o claude-usage-monitor .
```

## Usage

```bash
claude-usage-monitor                       # interactive TUI, refreshes every minute
claude-usage-monitor --interval 1m         # custom refresh interval
claude-usage-monitor --ping-interval 0     # no health ping (usage data only)
claude-usage-monitor --once                # one-shot plain text
claude-usage-monitor --once --ping         # ...including one health ping
claude-usage-monitor --json                # one-shot JSON (for scripting)
claude-usage-monitor --preview             # render one TUI frame to stdout
```

TUI keys: `r` to refresh now, `q` / `esc` / `ctrl-c` to quit.

JSON output (`health` appears only with `--ping`):

```json
{
  "timestamp": "2026-09-14T16:21:03Z",
  "five_hour": { "utilization": 0.02, "reset": 1789408800 },
  "seven_day": { "utilization": 0.83, "reset": 1789574400 },
  "scoped_weekly": [
    { "label": "Fable", "utilization": 1, "reset": 1789574400,
      "severity": "critical", "active": true }
  ],
  "seven_day_breakdown": [
    { "label": "Claude Code", "percent": 96 },
    { "label": "Chats", "percent": 1 }
  ],
  "extra_usage_enabled": false,
  "health": { "kind": "ok", "latency_ms": 621000000, "checked_at": "2026-09-14T16:21:03Z",
              "status_page": { "indicator": "none", "description": "All Systems Operational" } }
}
```

Utilizations are fractions (0–1); the API reports them as 0–100 and they are
converted on the way in.

## Authentication

Reads the OAuth access token from `~/.claude/.credentials.json` (the file Claude Code writes when you `claude login`).

Subscription tokens are OAuth bearers, not API keys — the probe sends them as `Authorization: Bearer <token>` together with `anthropic-beta: oauth-2025-04-20`. Sent as `x-api-key` they get a flat `401`.

The credentials file is re-read on every refresh, so when Claude Code rotates the access token a running TUI picks up the new one instead of dying at expiry.

If you don't have Claude Code installed, log in once at <https://claude.ai/code> via the CLI to populate the credentials file.

## Cost

Reading usage costs nothing — it is a GET, not an inference call, so the refresh
loop is free.

It is not unlimited, though: the endpoint has its own request budget and answers
`429` if polled too hard. The default interval is one minute, and a failed fetch
backs off — the server's `Retry-After` when it sends one, otherwise doubling from
the interval up to five minutes, reset by the first success.

The health ping is a real request (8 input tokens, 1 output token) and does land in
the 5-hour window this tool reports. At the default 30-minute interval that is 48
calls a day, which is negligible but not zero. `--ping-interval 0` turns it off.

## Requirements

- Go 1.26+ (to build)
- A Claude Max or Pro subscription
- Claude Code installed and logged in (for the credentials file)
