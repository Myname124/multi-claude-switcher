package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/miou1107/multi-claude-switcher/core"
)

// appZipPrefix / appZipSuffix bracket the release asset the updater keys on (e.g.
// "Multi-Claude-Switcher_0.6.1_macos.zip" on macOS, the setup.exe on Windows).
// The version sits in the middle, so we match by prefix+suffix rather than an
// exact name. appZipSuffix is OS-specific and defined in update_platform_*.go.
const appZipPrefix = "Multi-Claude-Switcher_"

// findAppZip returns the download URL of the platform's release asset.
func findAppZip(assets map[string]string) (string, bool) {
	for name, url := range assets {
		if strings.HasPrefix(name, appZipPrefix) && strings.HasSuffix(name, appZipSuffix) {
			return url, true
		}
	}
	return "", false
}

// updating single-flights the check/apply pipeline. Overlapping runs (a manual
// "Check for Updates…" landing during the startup/6h auto check, or a rapid
// double-click) would otherwise both write exe+".new" and could swap in a
// corrupt binary with no rollback. A second run bails immediately.
var updating sync.Mutex

// consecutiveCheckFailures counts background checks that could not reach
// GitHub, and resets on the first that can. Guarded by `updating`, which every
// read and write below is already inside.
//
// checkFailuresBeforeNotifying is deliberately larger than one: checks run at
// startup and then every 6 hours, so three failures means roughly half a day
// of not reaching GitHub, which is past any plausible transient. The notice
// fires once per run of failures, not once per failure — the counter passes
// the threshold exactly once on its way up.
var consecutiveCheckFailures int

const checkFailuresBeforeNotifying = 3

// shouldWarnAboutFailedChecks reports whether this failure is the one to say
// something about. Split out so the "once per run of failures, not once per
// failure" rule is testable without a network, a tray, or a 6-hour wait —
// equality, not >=, is the whole of that rule and is easy to get wrong.
func shouldWarnAboutFailedChecks(consecutive int) bool {
	return consecutive == checkFailuresBeforeNotifying
}

// startUpdateChecker checks for a newer release at startup (after a short delay)
// and then periodically. `auto` runs are quiet on "already up to date".
func startUpdateChecker() {
	announceUpdateIfNew()
	go func() {
		time.Sleep(8 * time.Second) // let the menu settle first
		checkForUpdate(true)
		for range time.Tick(6 * time.Hour) {
			checkForUpdate(true)
		}
	}()
}

// checkForUpdate looks for a newer release and, if found, hands it to the
// platform-specific installUpdate. When auto is false (manual "Check for
// Updates") it also reports the "up to date" and error cases to the user.
func checkForUpdate(auto bool) {
	if !updating.TryLock() {
		log.Printf("Update already in progress; skipping this check")
		if !auto {
			notify("正在更新", "已经有一个更新在进行中。")
		}
		return
	}
	defer updating.Unlock()

	tag, assets, err := core.LatestRelease()
	if err != nil {
		log.Printf("Update check failed: %v", err)
		if !auto {
			go infoDialog("Multi-Claude Switcher", "无法检查更新：\n"+err.Error())
			return
		}
		// A background check stays quiet about a single failure: the network
		// comes and goes, and a toast every 6 hours over a transient DNS blip
		// would be worse than saying nothing. But staying quiet forever is a
		// blind spot with no floor. Observed on a real machine: seven
		// consecutive "lookup api.github.com: no such host" over two days,
		// with the app sitting two minor versions behind and nothing on
		// screen ever suggesting so. It only updated because the user opened
		// Settings and pressed the button by hand.
		consecutiveCheckFailures++
		if shouldWarnAboutFailedChecks(consecutiveCheckFailures) {
			notify("检查更新一直失败",
				"Multi-Claude Switcher 已经有一段时间连不上 GitHub，可能不是最新版本。")
		}
		return
	}
	consecutiveCheckFailures = 0
	if !core.IsNewer(tag, core.Version) {
		log.Printf("Up to date (current v%s, latest %s)", core.Version, tag)
		if !auto {
			go infoDialog("Multi-Claude Switcher", "已经是最新版本（v"+core.Version+"）。\n没有需要安装的更新。")
		}
		return
	}

	url, ok := findAppZip(assets)
	if !ok {
		log.Printf("Release %s has no downloadable asset for this platform (%s…%s); cannot update", tag, appZipPrefix, appZipSuffix)
		if !auto {
			go infoDialog("Multi-Claude Switcher", "新版本 "+tag+" 已发布，但还没有适用于这台电脑的下载。")
		}
		return
	}

	// Neither path asks before installing: a newer version is simply installed.
	// A check the user asked for shows the installer's progress bar, since they
	// are watching; a background one shows nothing. Either way the one
	// notification comes from the new version once it is running (see
	// announceUpdateIfNew).
	if err := installUpdate(url, tag, auto); err != nil {
		log.Printf("Update failed: %v", err)
		if auto {
			notify("更新失败", err.Error())
		} else {
			go infoDialog("Multi-Claude Switcher", "更新没能安装：\n"+err.Error())
		}
	}
	// On success installUpdate relaunches and quits: macOS swaps the bundle, Windows
	// runs the downloaded setup.exe unattended and exits so it can replace the
	// running executable. Either way there is nothing more to do here.
	//
	// Windows opens the releases page only when an update was found and could NOT be
	// installed, and only for a check the user asked for (see openURL in
	// update_install_windows.go). This comment used to say the download page was the
	// normal Windows outcome, which is how the silent installer looked like it had
	// regressed when it had not.
}

// lastVersionFile records the version that last started, so the first start
// after an update can say so. It lives beside the logs.
func lastVersionFile() string {
	return filepath.Join(filepath.Dir(core.LogDir()), "last-version")
}

// noteVersionStarted records current as the last version to start and returns
// the version recorded before it, and whether current is newer than that. A
// missing or unreadable record (first install) is not an upgrade: there is
// nothing to announce to someone who just installed the app.
func noteVersionStarted(path, current string) (previous string, upgraded bool) {
	if b, err := os.ReadFile(path); err == nil {
		previous = strings.TrimSpace(string(b))
	}
	if previous != current {
		if err := os.WriteFile(path, []byte(current+"\n"), 0o644); err != nil {
			log.Printf("could not record the running version in %s: %v", path, err)
		}
	}
	return previous, previous != "" && core.IsNewer(current, previous)
}

// announceUpdateIfNew tells the user, once, that the app they are now running
// is the version an update just installed. It is the only notification an
// update produces, and a toast rather than a dialog: after a background update
// nobody is waiting for it, and after a manual one the user already watched the
// progress bar.
func announceUpdateIfNew() {
	previous, upgraded := noteVersionStarted(lastVersionFile(), core.Version)
	if !upgraded {
		return
	}
	log.Printf("Started on v%s after updating from v%s", core.Version, previous)
	notify("Multi-Claude Switcher 已更新", fmt.Sprintf("现在是 v%s（原来是 v%s）。", core.Version, previous))
}
