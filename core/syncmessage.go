package core

import (
	"errors"
	"fmt"
)

// SyncResultMessage is the sentence the panel shows after a sync.
//
// It lives here, not in each host, because both the macOS menu bar and the
// Windows tray show it and they must not word it differently. They share a
// renderer for the same reason. Keeping it in core also makes it testable: the
// cmd packages have no tests, and this string is the only place a clash is ever
// reported to a user of the panel.
func SyncResultMessage(rep *SyncReport, targetDisplay string) string {
	summary, skipped := SyncResultParts(rep, targetDisplay)
	if skipped == "" {
		return summary
	}
	return summary + skipped
}

// SyncResultParts splits that sentence in two: what the sync did, and what it
// could not read.
//
// The panel needs them apart. Files that could not be read are a warning, and
// the progress card puts a warning in its own box and then waits to be closed,
// rather than clearing itself after two seconds like a clean result does. Both
// halves still come from here so the card and the one-line message cannot end
// up wording the same fact differently.
func SyncResultParts(rep *SyncReport, targetDisplay string) (summary, skipped string) {
	if rep == nil {
		return "同步完成。", ""
	}
	// No tick in the text: the progress card draws its own, and two in a row
	// reads as a stutter. The one-line banner this also feeds never needed it to
	// be understood.
	summary = fmt.Sprintf("已复制 %s到 %s。", pluralConversations(rep.CopiedCount), targetDisplay)
	if rep.ConflictCount > 0 {
		// The target already had a newer version of these, so they were left
		// alone. Worth saying, because otherwise a sync that copied little looks
		// like a sync that did nothing.
		summary += "另有 " + pluralConversations(rep.ConflictCount) + "在这边已经是更新的版本，保持不动。"
	}
	if n := len(rep.SkipErrors); n > 0 {
		// These could not be read or written at all. A count with a pointer to the
		// log beats silence: the run continued past them on purpose, so nothing
		// else would ever mention them.
		skipped = fmt.Sprintf("有 %d 个文件读不了，已跳过（详见日志）。", n)
	}
	return summary, skipped
}

// pluralConversations renders a count of conversations with its measure word.
func pluralConversations(n int) string {
	return fmt.Sprintf("%d 个对话", n)
}

// SyncFailureMessage turns a sync error into something a non-technical user can
// act on. Only the cases a user can actually do something about are translated;
// anything else is passed through so a real fault is never hidden.
func SyncFailureMessage(err error) string {
	reason := SyncFailureReason(err)
	if reason == "" || errors.Is(err, ErrRunningProfileUnknown) {
		// The translated message below is already a complete instruction;
		// prefixing it would read as "Sync failed: quit Claude Desktop first".
		return reason
	}
	return "同步失败：" + reason
}

// SyncFailureReason is the same text without the "Sync failed" prefix, for a
// caller that has already said so.
//
// The progress card has "Sync failed" as its heading, so the prefixed form put
// it on screen twice. Splitting it here rather than trimming the prefix at the
// call site keeps one source for the wording, the same way SyncResultParts does
// for the success case.
func SyncFailureReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrRunningProfileUnknown) {
		// Reached by anyone who launched Claude Desktop themselves instead of
		// through MCS. The underlying reason (no --user-data-dir to match on) is
		// not something to put in front of a user; the action is.
		return "请先退出 Claude Desktop，然后再试一次同步。"
	}
	return err.Error()
}
