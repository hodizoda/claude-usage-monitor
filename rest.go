package main

import (
	"fmt"
	"time"
)

// restingCat is shown when the account is rate limited. There is nothing to do
// but wait, so the screen says so plainly.
const restingCat = `     |\      _,,,---,,_
     /,` + "`" + `.-'` + "`" + `'    -.  ;-;;,_
    |,4-  ) )-,_. ,\ (  ` + "`" + `'-'
   '---''(_/--'  ` + "`" + `-'\_)`

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
		if c.util < 0.995 || c.reset == 0 {
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
