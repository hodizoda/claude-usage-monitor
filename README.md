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

The Claude Code CLI's `/usage` dialog isn't backed by a separate API — the data comes back as response headers on every `/v1/messages` call:

| Header | Meaning |
|--------|---------|
| `anthropic-ratelimit-unified-5h-utilization` | Fraction of the 5-hour window used |
| `anthropic-ratelimit-unified-5h-reset` | Unix timestamp when the 5-hour window resets |
| `anthropic-ratelimit-unified-7d-utilization` | Fraction of the 7-day window used |
| `anthropic-ratelimit-unified-7d-reset` | Unix timestamp when the 7-day window resets |
| `anthropic-ratelimit-unified-status` | `allowed` or `rejected` |
| `anthropic-ratelimit-unified-representative-claim` | Which window is the binding constraint |
| `anthropic-ratelimit-unified-overage-status` | Overage allowed / rejected |

This tool makes a 1-token Haiku call (the cheapest possible request) and reads those headers. No private endpoints, no scraping.

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
claude-usage-monitor                    # interactive TUI, refreshes every 30s
claude-usage-monitor --interval 1m      # custom refresh interval
claude-usage-monitor --once             # one-shot plain text
claude-usage-monitor --json             # one-shot JSON (for scripting)
claude-usage-monitor --preview          # render one TUI frame to stdout
```

TUI keys: `r` to refresh now, `q` / `esc` / `ctrl-c` to quit.

JSON output:

```json
{
  "timestamp": "2026-04-17T09:58:46Z",
  "status": "allowed",
  "five_hour_status": "allowed",
  "five_hour_reset": 1776420000,
  "five_hour_utilization": 0.2,
  "seven_day_status": "allowed",
  "seven_day_reset": 1776970800,
  "seven_day_utilization": 0.06,
  "representative_claim": "five_hour",
  "fallback_percentage": 0.5,
  "overage_status": "rejected",
  "overage_disabled_reason": "org_level_disabled"
}
```

## Authentication

Reads the OAuth access token from `~/.claude/.credentials.json` (the file Claude Code writes when you `claude login`). The token is used as `x-api-key` on the probe call.

If you don't have Claude Code installed, log in once at <https://claude.ai/code> via the CLI to populate the credentials file.

## Cost

Each refresh costs one minimum-size Haiku request (8 input tokens, 1 output token). At default 30-second refresh that's ~120 calls/hour — well under a cent per day.

## Requirements

- Go 1.26+ (to build)
- A Claude Max or Pro subscription
- Claude Code installed and logged in (for the credentials file)
