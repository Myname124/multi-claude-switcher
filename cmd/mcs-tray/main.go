package main

import (
	"log"
	"os"
	"os/exec"

	"fyne.io/systray"
	"github.com/miou1107/multi-claude-switcher/core"
	"github.com/miou1107/multi-claude-switcher/platform"
)

var (
	plat     platform.Platform
	bm       *core.BackupManager
	switcher *core.Switcher
)

func main() {
	// Panel mode: this same binary launched with --panel is the WebView2
	// popover for Windows (see panel_windows.go). It runs to completion in
	// its own process so it doesn't fight systray for the main thread, then
	// exits when the panel loses focus.
	if len(os.Args) > 1 && os.Args[1] == "--panel" {
		runPanel()
		return
	}

	if closer, _, err := core.SetupLogging("mcs-tray"); err == nil {
		defer closer.Close()
	} else {
		log.Printf("Warning: could not open log file, logging to stderr only: %v", err)
	}

	// Refuse to start a second tray instance (two icons / two auto-updaters).
	// The self-updater relaunch is exempt via the skip flag, since the old
	// instance is still quitting when the new one starts.
	if !hasSkipInstanceFlag(os.Args) && anotherInstanceRunning() {
		log.Printf("Another Multi-Claude Switcher tray is already running; exiting.")
		notify("Multi-Claude Switcher 已经在运行", "它已经在系统托盘里了，这个多开的副本会关闭。")
		return
	}

	plat = platform.New()
	bm = core.NewBackupManager("")
	switcher = core.NewSwitcher(plat, bm)
	core.StartTidyMisfiled(plat) // one-off cleanup of what the pre-0.11.2 sync misfiled

	systray.Run(onReady, onExit)
}

// onReady hands over to the Windows tray: the icon, a small right-click menu,
// and the WebView2 panel spawned by `mcs-tray.exe --panel`, which holds the
// actual UI (account list, switch, sync, rename, settings).
func onReady() {
	onReadyWindowsPanel()
}

func onExit() {
	// The tray keeps a warm panel process alive alongside itself; take it
	// down with us rather than orphaning it.
	stopPanelProcess()
	// The tray also repoints the claude:// handler at the active profile while
	// it runs. Put it back, so the switcher leaves nothing behind once it is
	// not around to maintain it.
	restoreProtocolHandler()
	log.Println("Multi-Claude Switcher Tray exited cleanly.")
}

// relaunchSelf starts a fresh copy of the tray (detached, with the instance-check
// skip flag) and quits this one. Used after an action that changes the profile
// set, since the systray menu is built once at startup and cannot grow new items
// afterward.
func relaunchSelf() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("relaunch: cannot find own executable: %v", err)
		return
	}
	args := append([]string{}, os.Args[1:]...)
	if !hasSkipInstanceFlag(args) {
		args = append(args, relaunchSkipInstanceCheckFlag)
	}
	cmd := exec.Command(exe, args...)
	detachRelaunch(cmd) // detach so it outlives us
	if err := cmd.Start(); err != nil {
		log.Printf("relaunch failed: %v", err)
		return
	}
	systray.Quit()
}
