package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styling — modelled after the Claude web "Plan usage limits" card.
var (
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(1, 3).
			Width(64)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)

	sectionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true).
			MarginTop(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	percentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)

	barFilledStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4A90E2"))

	barFilledWarnStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F5A524"))

	barFilledDangerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E5484D"))

	barEmptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("237"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			MarginTop(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5484D"))
)

type tickMsg time.Time
type fetchResultMsg struct {
	info *RateLimitInfo
	err  error
}

type model struct {
	apiKey       string
	subscription string
	tier         string
	interval     time.Duration

	info        *RateLimitInfo
	err         error
	fetching    bool
	lastFetch   time.Time
	nextFetchAt time.Time
	quit        bool
}

func newModel(apiKey, subscription, tier string, interval time.Duration) model {
	return model{
		apiKey:       apiKey,
		subscription: subscription,
		tier:         tier,
		interval:     interval,
		fetching:     true,
		nextFetchAt:  time.Now().Add(interval),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), m.tickCmd())
}

func (m model) fetchCmd() tea.Cmd {
	return func() tea.Msg {
		info, err := fetchUsage(m.apiKey)
		return fetchResultMsg{info: info, err: err}
	}
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		case "r":
			if !m.fetching {
				m.fetching = true
				m.nextFetchAt = time.Now().Add(m.interval)
				return m, m.fetchCmd()
			}
		}

	case fetchResultMsg:
		m.fetching = false
		m.lastFetch = time.Now()
		m.nextFetchAt = m.lastFetch.Add(m.interval)
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.info = msg.info
		}

	case tickMsg:
		// Every second: redraw for the live countdown. If it's time to refetch
		// and we aren't already, fire off a new fetch.
		if !m.fetching && time.Now().After(m.nextFetchAt) {
			m.fetching = true
			return m, tea.Batch(m.fetchCmd(), m.tickCmd())
		}
		return m, m.tickCmd()
	}
	return m, nil
}

// bar renders a progress bar like "████░░░░░░░░░░" at the given width.
// Color transitions at 50% and 80%.
func bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(width))
	// Ensure any non-zero utilization shows at least one filled cell.
	if filled == 0 && pct > 0 {
		filled = 1
	}
	empty := width - filled

	fs := barFilledStyle
	switch {
	case pct >= 0.8:
		fs = barFilledDangerStyle
	case pct >= 0.5:
		fs = barFilledWarnStyle
	}

	return fs.Render(strings.Repeat("█", filled)) +
		barEmptyStyle.Render(strings.Repeat("█", empty))
}

// row renders one usage row, matching the Claude web UI:
//
//	<label>                                     <pct>% used
//	<reset hint>
//	<bar>
func row(label, resetHint string, pct float64, innerW int) string {
	pctText := fmt.Sprintf("%.0f%% used", pct*100)

	// Top line: label (normal) left, "% used" (bold) right.
	spaces := innerW - lipgloss.Width(label) - lipgloss.Width(pctText)
	if spaces < 1 {
		spaces = 1
	}
	top := labelStyle.Render(label) + strings.Repeat(" ", spaces) + percentStyle.Render(pctText)

	reset := dimStyle.Render(resetHint)
	b := bar(pct, innerW)

	return top + "\n" + reset + "\n" + b
}

func (m model) View() string {
	if m.quit {
		return ""
	}

	innerW := 58

	var body strings.Builder

	body.WriteString(titleStyle.Render("Plan usage limits"))
	body.WriteString("\n")

	if m.err != nil && m.info == nil {
		body.WriteString("\n")
		body.WriteString(errorStyle.Render("Error: " + m.err.Error()))
		body.WriteString("\n")
	} else if m.info == nil {
		body.WriteString("\n")
		body.WriteString(dimStyle.Render("Loading…"))
		body.WriteString("\n")
	} else {
		info := m.info

		// Current session (5-hour window)
		body.WriteString(sectionStyle.Render("Current session"))
		body.WriteString("\n")
		body.WriteString(row(
			"5-hour window",
			"Resets in "+formatResetRelative(info.FiveHourReset),
			info.FiveHourUtilization,
			innerW,
		))
		body.WriteString("\n")

		// Weekly limits (7-day window)
		body.WriteString(sectionStyle.Render("Weekly limits"))
		body.WriteString("\n")
		body.WriteString(row(
			"All models",
			"Resets "+formatResetAbsolute(info.SevenDayReset),
			info.SevenDayUtilization,
			innerW,
		))
		body.WriteString("\n")

		// Status line
		var status strings.Builder
		status.WriteString("Status: ")
		switch info.Status {
		case "allowed":
			status.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#30A46C")).Render("allowed"))
		case "rejected":
			status.WriteString(errorStyle.Render("rejected"))
		default:
			status.WriteString(info.Status)
		}
		status.WriteString(dimStyle.Render(
			fmt.Sprintf(" · binding: %s · overage: %s",
				info.RepresentativeClaim, info.OverageStatus)))
		body.WriteString("\n")
		body.WriteString(status.String())
	}

	// Footer: two halves, subscription left, status/keys right.
	sub := m.subscription
	if m.tier != "" {
		// Abbreviate "default_claude_max_5x" → "5x".
		tier := m.tier
		if idx := strings.LastIndex(tier, "_"); idx >= 0 {
			tier = tier[idx+1:]
		}
		sub = sub + " (" + tier + ")"
	}

	var rightSide string
	switch {
	case m.err != nil && m.info != nil:
		rightSide = errorStyle.Render("↻ failed") + dimStyle.Render(" · [r]etry [q]uit")
	case m.fetching:
		rightSide = dimStyle.Render("↻ refreshing… · [q]uit")
	default:
		until := time.Until(m.nextFetchAt).Round(time.Second)
		if until < 0 {
			until = 0
		}
		rightSide = dimStyle.Render(fmt.Sprintf("↻ %s · [r] [q]", until))
	}

	leftSide := dimStyle.Render(sub)
	spaces := innerW - lipgloss.Width(leftSide) - lipgloss.Width(rightSide)
	if spaces < 1 {
		spaces = 1
	}
	footer := leftSide + strings.Repeat(" ", spaces) + rightSide
	body.WriteString("\n\n")
	body.WriteString(footerStyle.Render(footer))

	// Pad each line out to innerW so the card right-edge lines up.
	lines := strings.Split(body.String(), "\n")
	for i, line := range lines {
		w := lipgloss.Width(line)
		if w < innerW {
			lines[i] = line + strings.Repeat(" ", innerW-w)
		}
	}
	return cardStyle.Render(strings.Join(lines, "\n"))
}

func runTUI(apiKey, subscription, tier string, interval time.Duration) error {
	m := newModel(apiKey, subscription, tier, interval)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
