# claude-usage-monitor

A terminal UI for a Claude Pro or Max subscription's usage limits: the 5-hour window,
the weekly window, any per-model weekly limit, and when each one resets — the same
numbers as the "Plan usage limits" panel in Claude and the `/usage` dialog in Claude
Code. It also checks that the API is actually answering, and tells you to rest when a
limit is spent.

```
╭────────────────────────────────────────────────────────────────╮
│                                                                │
│   Plan usage limits                                            │
│                                                                │
│   Current session                                              │
│   5-hour window                                     38% used   │
│   Resets in 2 hr 14 min                                        │
│   ██████████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   │
│                                                                │
│   Weekly limits                                                │
│   All models                                        83% used   │
│   Resets Fri at 2:38 AM                                        │
│   ████████████████████████████████████████████████░░░░░░░░░░   │
│   Fable only                                       100% used   │
│   Resets Fri at 2:38 AM                                        │
│   ██████████████████████████████████████████████████████████   │
│                                                                │
│   Claude Code 96% · Chats 1% · Other 3%                        │
│                                                                │
│        |\      _,,,---,,_                                      │
│        /,`.-'`'    -.  ;-;;,_                                  │
│       |,4-  ) )-,_. ,\ (  `'-'                                 │
│      '---''(_/--'  `-'\_)                                      │
│   Cool down 1d 16h — Fable weekly limit resets. Rest.          │
│   API ok · 581ms · just now                                    │
│                                                                │
│                                                                │
│   max (20x)                                ↻ 7m42s · [r] [q]   │
│                                                                │
╰────────────────────────────────────────────────────────────────╯
```

In a terminal the bars are two-tone and change colour at 50% and 80%, and the cat is
painted on a gradient.

## How it works

Usage comes from `GET /api/oauth/usage`, the endpoint behind Claude Code's own
`/usage` dialog. It is a plain authenticated GET — no inference call, so reading it
costs nothing and adds nothing to the numbers it reports.

It returns the 5-hour window, the all-models weekly window, any **per-model weekly
limit** ("Current week (Fable)"), a breakdown of where the week went by surface, and
the extra-usage state.

The plan in the footer comes from `GET /api/oauth/profile`, not from the credentials
file. That file records the rate-limit tier as it was at the last `claude login`, so
an upgraded account keeps reading as its old tier until the next login. Only the
organization's plan and tier are decoded from the response. If the call fails, the
credentials file is the fallback.

An earlier version read `anthropic-ratelimit-unified-*` response headers off a 1-token
Haiku call. Those headers still exist, but they carry no per-model window, and the
probe itself costs usage.

## Refresh and rate limits

The usage endpoint is free but not unlimited: it has a small request budget, shared
with Claude Code's own polling of it for the statusline, and answers `429` when that
runs out. Refreshing every 30–50 seconds hit it often.

So each refresh waits a delay picked at random from **3, 5, 8 or 13 minutes**. The
numbers move slowly, the spread keeps the tool out of step with any other client, and
`r` refreshes immediately whenever you want a fresh reading.

When a fetch fails, the next attempt backs off: a normal interval first, then doubling
up to a 30-minute ceiling, reset by the first success. A server `Retry-After` can make
the wait longer but never shorter — the endpoint has been seen sending
`retry-after: 0` while still refusing. A rate-limited fetch shows as one line,
`Rate limited — backing off`, with the time of the next attempt; the last good numbers
stay on screen.

## Health check

Usage data says nothing about whether the API is answering, so a separate, much slower
ping (`--ping-interval`, default 30 minutes) sends one minimum-size Haiku request and
classifies the result:

| Result | Meaning |
|--------|---------|
| `ok` | Inference answered; the round-trip time is shown |
| `limited` | HTTP 429 — the account is capped. **Not** an outage |
| `auth` | 401/403 — the token, not the service |
| `down` | 5xx, timeout, or no route to the API |

`https://status.claude.com/api/v2/status.json` is read alongside it, with no auth and
no tokens. An ongoing incident is shown even when the ping succeeds, and if the ping
fails while the status page reports no incident, the tool says the fault is local
rather than blaming Anthropic.

## Cool down

When any window is spent — the 5-hour one, the weekly one, or a per-model weekly limit
— the card shows a countdown and a resting cat. The usage data alone raises it; a 429
from the health ping does too. Waiting for a refused request would mean the
interruption had already happened.

The window named is the one actually blocking. Every window at or above 99.5% is a
candidate and the soonest to reset wins, so a full per-model weekly limit is not
reported as a 5-hour wait.

A `429` from the usage endpoint does **not** raise the cat. That is the endpoint's
request budget, not a subscription limit, so there is no window to wait out.

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
claude-usage-monitor                        # TUI, refreshes every 3/5/8/13 min at random
claude-usage-monitor --interval 2m          # fixed refresh period
claude-usage-monitor --interval 1m,2m,4m    # your own set to pick from
claude-usage-monitor --ping-interval 0      # no health ping, usage data only
claude-usage-monitor --once                 # one-shot plain text
claude-usage-monitor --once --ping          # ...with one health ping
claude-usage-monitor --json                 # one-shot JSON, for scripting
claude-usage-monitor --preview              # render one TUI frame to stdout
```

TUI keys: `r` refreshes now, `q` / `esc` / `ctrl-c` quits.

`--interval` refuses values under 10 seconds and malformed lists rather than falling
back to a default.

JSON output (`health` appears only with `--ping`):

```json
{
  "timestamp": "2026-09-14T16:21:03Z",
  "five_hour": { "utilization": 0.38, "reset": 1789408800 },
  "seven_day": { "utilization": 0.83, "reset": 1789574400 },
  "scoped_weekly": [
    { "label": "Fable", "utilization": 1, "reset": 1789574400,
      "severity": "critical", "active": true }
  ],
  "seven_day_breakdown": [
    { "label": "Claude Code", "percent": 96 },
    { "label": "Chats", "percent": 1 },
    { "label": "Other", "percent": 3 }
  ],
  "extra_usage_enabled": false,
  "health": {
    "kind": "ok",
    "latency_ms": 581,
    "checked_at": "2026-09-14T16:21:03Z",
    "status_page": { "indicator": "none", "description": "All Systems Operational" }
  }
}
```

Utilizations are fractions from 0 to 1. The API reports them from 0 to 100, and they
are converted on the way in. Resets are Unix seconds.

## Authentication

The OAuth access token is read from `~/.claude/.credentials.json`, the file Claude Code
writes on `claude login`.

Subscription tokens are OAuth bearers, not API keys. Every request sends them as
`Authorization: Bearer <token>` with `anthropic-beta: oauth-2025-04-20`; sent as
`x-api-key` they get a flat `401`.

The credentials file is re-read on every request, so when Claude Code rotates the
token, a running TUI picks up the new one instead of failing at expiry.

## Cost

Reading usage and the profile costs nothing: both are GETs, not inference calls.

The health ping is a real request — 8 input tokens, 1 output token — and it does land
in the 5-hour window this tool reports. At the default 30-minute interval that is 48
calls a day: negligible, but not zero. `--ping-interval 0` turns it off.

## Requirements

- Go 1.26.0 or newer, to build
- A Claude Pro or Max subscription
- Claude Code installed and logged in, for the credentials file
