package panelui

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/miou1107/multi-claude-switcher/core/usage"
	"github.com/miou1107/multi-claude-switcher/platform"
)

// UsageVM is what an account card shows about its rate limits. A nil bar
// means that window is unknown, which is drawn as nothing rather than as 0%.
type UsageVM struct {
	FiveHour *UsageBarVM
	Week     *UsageBarVM
	// Note is a muted line under the bars: why the reading could not be
	// refreshed, or how old it is. Empty when the reading is current.
	Note string
}

// UsageBarVM is one window, ready to draw.
type UsageBarVM struct {
	Percent int
	// Resets is "resets 15:00" or "resets Mon 09:00", empty when unknown.
	Resets string
}

// staleAfter is how old a reading may be before the card says when it was
// taken. A panel opened right after launch shows the reading saved last time
// until the refresh lands, and should not present it as live.
const staleAfter = 10 * time.Minute

// AttachUsage fills in each signed-in profile's usage from lookup. Both hosts
// call it on BuildProfiles' result, so neither can forget a field.
func AttachUsage(vms []ProfileVM, lookup func(accountUUID string) (usage.Entry, bool), now time.Time) []ProfileVM {
	for i := range vms {
		if vms[i].UUID == "" {
			continue
		}
		if e, ok := lookup(vms[i].UUID); ok {
			vms[i].Usage = usageVM(e, now)
		}
	}
	return vms
}

// UsageTargets lists the listed, signed-in profiles whose usage a refresh
// should read. Only accounts on the list are read: a folder the user chose not
// to manage is none of the panel's business.
func UsageTargets(vms []ProfileVM, profiles []*platform.ProfileInfo) []usage.Target {
	pathOf := map[string]string{}
	for _, p := range profiles {
		pathOf[p.Name] = p.Path
	}
	var out []usage.Target
	for _, p := range vms {
		if p.UUID == "" || pathOf[p.Folder] == "" {
			continue
		}
		out = append(out, usage.Target{AccountUUID: p.UUID, ProfilePath: pathOf[p.Folder]})
	}
	return out
}

func usageVM(e usage.Entry, now time.Time) *UsageVM {
	vm := &UsageVM{}
	if e.Usage != nil {
		vm.FiveHour = barVM(e.Usage.FiveHour, now)
		vm.Week = barVM(e.Usage.SevenDay, now)
	}
	switch {
	case e.Err != "" && e.Usage != nil:
		vm.Note = "As of " + clock(e.FetchedAt, now) + ". " + e.Err
	case e.Err != "":
		vm.Note = e.Err
	case e.Usage != nil && now.Sub(e.FetchedAt) > staleAfter:
		vm.Note = "As of " + clock(e.FetchedAt, now) + "."
	}
	if vm.FiveHour == nil && vm.Week == nil && vm.Note == "" {
		return nil
	}
	return vm
}

func barVM(w *usage.Window, now time.Time) *UsageBarVM {
	if w == nil {
		return nil
	}
	eff := w.Effective(now)
	out := &UsageBarVM{Percent: int(math.Round(eff.Percent))}
	if !eff.ResetsAt.IsZero() {
		out.Resets = "resets " + clock(eff.ResetsAt, now)
	}
	return out
}

// clock is a time as the panel writes it: "15:00" within a day of now, else
// with the weekday, else with the date.
func clock(t, now time.Time) string {
	t = t.Local()
	d := t.Sub(now)
	if d < 0 {
		d = -d
	}
	switch {
	case d < 20*time.Hour:
		return t.Format("15:04")
	case d < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	default:
		return t.Format("Jan 2")
	}
}

// usageLevel picks the bar colour: the same green as "in use", then amber,
// then the red the panel already uses for problems.
func usageLevel(p int) string {
	switch {
	case p >= 90:
		return "u-hi"
	case p >= 70:
		return "u-mid"
	default:
		return "u-ok"
	}
}

// usageBlock renders a card's usage, or "" when there is nothing to show.
func usageBlock(u *UsageVM) string {
	if u == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="usage viewrow">`)
	row := func(label string, bar *UsageBarVM) {
		if bar == nil {
			return
		}
		fmt.Fprintf(&b, `<div class="urow"><span class="ulabel">%s</span><span class="ubar"><i class="%s" style="width:%d%%"></i></span><span class="upct">%d%%</span><span class="ureset">%s</span></div>`,
			label, usageLevel(bar.Percent), bar.Percent, bar.Percent, html.EscapeString(bar.Resets))
	}
	row("5h", u.FiveHour)
	row("Week", u.Week)
	if u.Note != "" {
		b.WriteString(`<div class="unote">` + html.EscapeString(u.Note) + `</div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
