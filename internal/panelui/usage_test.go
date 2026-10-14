package panelui

import (
	"strings"
	"testing"
	"time"

	"github.com/miou1107/multi-claude-switcher/core/usage"
	"github.com/miou1107/multi-claude-switcher/platform"
)

func TestUsageVMShowsBarsResetsAndStaleness(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)
	e := usage.Entry{
		Usage: &usage.Usage{
			FiveHour: &usage.Window{Percent: 42.4, ResetsAt: now.Add(3 * time.Hour)},
			SevenDay: &usage.Window{Percent: 91, ResetsAt: now.Add(3 * 24 * time.Hour)},
		},
		FetchedAt: now.Add(-time.Minute),
	}
	vm := usageVM(e, now)
	if vm.FiveHour.Percent != 42 || vm.FiveHour.Resets != "15:00 重置" {
		t.Errorf("5h: %+v", vm.FiveHour)
	}
	if vm.Week.Percent != 91 || !strings.HasPrefix(vm.Week.Resets, "周六 ") {
		t.Errorf("week: %+v", vm.Week)
	}
	if vm.Note != "" {
		t.Errorf("a fresh reading carries a note: %q", vm.Note)
	}

	// Old reading: the 5-hour window has reset since, so it reads 0 with no
	// reset time, and the card says how old the reading is.
	later := now.Add(4 * time.Hour)
	vm = usageVM(e, later)
	if vm.FiveHour.Percent != 0 || vm.FiveHour.Resets != "" {
		t.Errorf("reset window: %+v", vm.FiveHour)
	}
	if vm.Week.Percent != 91 {
		t.Errorf("week should be unchanged: %+v", vm.Week)
	}
	if !strings.HasSuffix(vm.Note, " 时的数据。") {
		t.Errorf("stale reading not marked: %q", vm.Note)
	}

	// Failure with an earlier reading keeps the bars and says why.
	e.Err = "在 Claude 里打开一次这个账号即可刷新额度。"
	vm = usageVM(e, now)
	if vm.FiveHour == nil || !strings.Contains(vm.Note, e.Err) {
		t.Errorf("failure with reading: %+v", vm)
	}

	// Failure with nothing to show is the reason alone.
	vm = usageVM(usage.Entry{Err: "没有可用来读取额度的登录信息。"}, now)
	if vm.FiveHour != nil || vm.Week != nil || vm.Note == "" {
		t.Errorf("failure without reading: %+v", vm)
	}
}

func TestAttachUsageAndTargetsSkipSignedOut(t *testing.T) {
	vms := []ProfileVM{
		{Folder: "Claude", Name: "Work", UUID: "u1", SignedIn: true},
		{Folder: "Claude_New", Name: "New"},
	}
	lookup := func(uuid string) (usage.Entry, bool) {
		if uuid != "u1" {
			t.Errorf("looked up %q", uuid)
		}
		return usage.Entry{Usage: &usage.Usage{FiveHour: &usage.Window{Percent: 5}}, FetchedAt: time.Now()}, true
	}
	vms = AttachUsage(vms, lookup, time.Now())
	if vms[0].Usage == nil || vms[1].Usage != nil {
		t.Fatalf("attach: %+v / %+v", vms[0].Usage, vms[1].Usage)
	}

	profiles := []*platform.ProfileInfo{{Name: "Claude", Path: "/p/Claude"}, {Name: "Claude_New", Path: "/p/New"}}
	got := UsageTargets(vms, profiles)
	if len(got) != 1 || got[0].AccountUUID != "u1" || got[0].ProfilePath != "/p/Claude" {
		t.Fatalf("targets: %+v", got)
	}
}

func TestRenderListDrawsUsageOnBothCardKinds(t *testing.T) {
	u := &UsageVM{FiveHour: &UsageBarVM{Percent: 75, Resets: "15:00 重置"}, Week: &UsageBarVM{Percent: 10}, Note: "11:00 时的数据。"}
	page := RenderList([]ProfileVM{
		{Folder: "Claude", Name: "Work", Current: true, SignedIn: true, UUID: "a", Usage: u},
		{Folder: "Claude_P", Name: "Personal", SignedIn: true, UUID: "b", Usage: u},
		{Folder: "Claude_Q", Name: "None", SignedIn: true, UUID: "c"},
	}, false, "")
	if n := strings.Count(page, `<div class="usage viewrow">`); n != 2 {
		t.Fatalf("want usage on 2 cards, got %d", n)
	}
	for _, want := range []string{`class="u-mid" style="width:75%"`, `>75%<`, `15:00 重置`, `class="u-ok" style="width:10%"`, `11:00 时的数据。`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestUsageLevel(t *testing.T) {
	for p, want := range map[int]string{0: "u-ok", 69: "u-ok", 70: "u-mid", 89: "u-mid", 90: "u-hi", 100: "u-hi"} {
		if got := usageLevel(p); got != want {
			t.Errorf("%d: got %s want %s", p, got, want)
		}
	}
}
