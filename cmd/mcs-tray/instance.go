package main

import ()

// relaunchSkipInstanceCheckFlag is passed by the self-updater when it relaunches
// the app, so the fresh instance does not refuse to start while the old one is
// still quitting. It is a hidden internal flag, not a user-facing option.
const relaunchSkipInstanceCheckFlag = "--mcs-relaunch"

// hasSkipInstanceFlag reports whether the relaunch skip flag is in args.
func hasSkipInstanceFlag(args []string) bool {
	for _, a := range args {
		if a == relaunchSkipInstanceCheckFlag {
			return true
		}
	}
	return false
}
