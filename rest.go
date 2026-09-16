package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// restingCat is shown when the account is rate limited. There is nothing to do
// but wait, so the screen says so plainly.
const restingCat = `     |\      _,,,---,,_
     /,` + "`" + `.-'` + "`" + `'    -.  ;-;;,_
    |,4-  ) )-,_. ,\ (  ` + "`" + `'-'
   '---''(_/--'  ` + "`" + `-'\_)`

// capped reports whether some window is spent. This is the state that
// interrupts work, and it is visible from the usage data alone — waiting for a
// 429 to prove it means the interruption has already happened.
func capped(info *RateLimitInfo) bool {
	if info == nil {
		return false
	}
	if info.FiveHour.Utilization >= blockingAt || info.SevenDay.Utilization >= blockingAt {
		return true
	}
	for _, sl := range info.ScopedWeekly {
		if sl.Utilization >= blockingAt {
			return true
		}
	}
	return false
}

// blockingAt is where a window counts as spent: the API rounds, and 100% is
// reported as 1.0 slightly before the last token is gone.
const blockingAt = 0.995

// cooldown names the limit that is actually blocking and when it lifts.
// A window counts as blocking at 99.5% — the API rounds, and 100% is reported
// as 1.0 well before the last token is spent.
func cooldown(info *RateLimitInfo) (label string, reset int64) {
	type candidate struct {
		label string
		util  float64
		reset int64
	}
	candidates := []candidate{
		{"5-hour window", info.FiveHour.Utilization, info.FiveHour.Reset},
		{"weekly limit", info.SevenDay.Utilization, info.SevenDay.Reset},
	}
	for _, sl := range info.ScopedWeekly {
		candidates = append(candidates, candidate{sl.Label + " weekly limit", sl.Utilization, sl.Reset})
	}

	for _, c := range candidates {
		if c.util < blockingAt || c.reset == 0 {
			continue
		}
		// Several limits can be full at once; the soonest one to lift is the
		// one worth waiting for.
		if reset == 0 || c.reset < reset {
			label, reset = c.label, c.reset
		}
	}
	if reset == 0 {
		// Rate limited without a full window — the 5-hour clock is the one
		// that turns over next.
		return "5-hour window", info.FiveHour.Reset
	}
	return label, reset
}

// restMessage is the caption under the cat.
func restMessage(info *RateLimitInfo) string {
	label, reset := cooldown(info)
	if reset == 0 {
		return "Cool down — limit reached. Rest."
	}
	if time.Until(time.Unix(reset, 0)) <= 0 {
		return "Cool down over — " + label + " has reset."
	}
	return fmt.Sprintf("Cool down %s — %s resets. Rest.", formatResetRelative(reset), label)
}

// petPalette runs warm to cool — dusk over a sleeping cat. Stops are
// interpolated, so adding one changes the ramp without touching the renderer.
var petPalette = [][3]int{
	{0xF5, 0xA5, 0x24}, // amber
	{0xE5, 0x48, 0x4D}, // red
	{0xC0, 0x4A, 0xAE}, // magenta
	{0x8B, 0x5C, 0xF6}, // violet
}

// rampColor samples the palette at t in [0,1].
func rampColor(t float64) lipgloss.Color {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	span := float64(len(petPalette) - 1)
	pos := t * span
	i := int(pos)
	if i >= len(petPalette)-1 {
		i = len(petPalette) - 2
	}
	f := pos - float64(i)
	a, b := petPalette[i], petPalette[i+1]
	mix := func(n int) int { return int(float64(a[n]) + (float64(b[n])-float64(a[n]))*f) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", mix(0), mix(1), mix(2)))
}

// renderPet paints the art on a diagonal ramp, top-left to bottom-right.
// Spaces are left unstyled so the gradient costs nothing on empty cells.
func renderPet(art string) string {
	lines := strings.Split(art, "\n")
	widest := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > widest {
			widest = n
		}
	}
	if widest == 0 {
		return art
	}

	var out strings.Builder
	for y, line := range lines {
		for x, r := range []rune(line) {
			if r == ' ' {
				out.WriteRune(r)
				continue
			}
			// Average the two axes so the ramp reads diagonally.
			t := (float64(x)/float64(widest) + float64(y)/float64(max(1, len(lines)-1))) / 2
			out.WriteString(lipgloss.NewStyle().Foreground(rampColor(t)).Render(string(r)))
		}
		if y < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}
