package core

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/miou1107/multi-claude-switcher/platform"
)

// RemoveProfile takes an account off the switcher by moving its profile
// directory into the archive root, and returns where it landed.
//
// Nothing is deleted. The directory moves untouched, which is also why no backup
// is taken: a merge snapshots the profile it keeps because a merge writes into
// it, and this writes nothing.
//
// Order is resolve, refuse, move, then update state. The registries are written
// only once the folder has really left the scan path: a folder unmanaged while
// still in place is hidden from the panel and back on the next Rescan, so
// "removed" would not stay removed.
func RemoveProfile(plat platform.Platform, identity string) (string, error) {
	name := DisplayName(identity)

	// Platform refusals first, and the path they resolve to. This reads only, so a
	// refusal below still leaves the disk exactly as it was found.
	path, err := plat.PrepareRemove(identity)
	if err != nil {
		return "", err
	}
	if fi, statErr := os.Stat(path); statErr != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s 已经不在了。请重新扫描", name)
	}

	// Renaming a directory Claude has open is the one way this can corrupt data,
	// and POSIX rename will do it without complaint. Ask what is running rather
	// than consulting active.json: that record is empty on any machine where MCS
	// has not switched anything, which is exactly where a user who launched Claude
	// themselves would need the guard. Asking here, last before the rename, also
	// catches the panel that was drawn while Claude was closed.
	running, err := plat.DetectRunningProfiles()
	if err != nil {
		// Not knowing whether Claude holds the directory is not permission to
		// rename it, and a removal is never urgent enough to guess.
		return "", fmt.Errorf("无法确认 Claude 是否在运行，所以没有动 %s：%w", name, err)
	}
	for _, r := range running {
		if platform.SamePath(r, path) {
			return "", fmt.Errorf("Claude 正在使用 %s。请先切换到别的账号或退出 Claude，再移除它", name)
		}
	}

	dest, err := ArchiveProfile(identity, path, plat.ArchiveDir())
	if err != nil {
		// Nothing moved and no registry has been touched, so the account is still
		// listed and a retry is safe.
		return "", err
	}

	// From here the folder is gone from the scan path, so every registry naming it
	// is now wrong. Keep going past a failure: stopping at one would leave the
	// others describing a profile that no longer exists, which is worse than the
	// failure being reported. Every failure is returned, not merely logged, because
	// a display name left behind is silently inherited by any later profile that
	// reuses the identity and the user is the only one who can notice.
	// Each of these is written as a whole sentence, and says what the user has to
	// do about it, because errors.Join puts them on the screen one per line and
	// each one is read on its own. Three of the four need nothing done, and saying
	// so is the point: the panel lists the folders that exist and asks the
	// registries about those, so an entry naming a folder that is gone is never
	// drawn. The display name is the exception, and the only one worth an action.
	//
	// "Account", not "profile": a profile is the folder, and the user was never
	// shown that word anywhere else in the app.
	var errs []error
	if err := RemoveManaged(identity); err != nil {
		errs = append(errs, fmt.Errorf("切换器自己的账号列表里还记着它。不需要处理：面板只显示文件夹还在的账号。（%w）", err))
	}
	if err := SetProfileName(identity, ""); err != nil {
		errs = append(errs, fmt.Errorf("它的名字仍记录为 %q。以后再登录这个账号时会沿用这个名字，可以用「改名」修改。（%w）", name, err))
	}
	// Pending entries are pruned only on sign-in, so one left here would outlive
	// every folder it could describe.
	if err := RemovePending(identity); err != nil {
		errs = append(errs, fmt.Errorf("它的「等待登录」标记还在。不需要处理：这个标记只会显示在文件夹还在的账号上。（%w）", err))
	}
	// A stale active.json resolves to "" through lastActivatedPath and is harmless,
	// but it is one more record naming something that is gone.
	if LoadActiveProfile() == identity {
		if err := SaveActiveProfile(""); err != nil {
			errs = append(errs, fmt.Errorf("它仍被记录为你上次使用的账号。不需要处理：切换到任何账号都会替换这条记录。（%w）", err))
		}
	}
	if len(errs) > 0 {
		// The summary is its own line rather than a "..., but" clause running into
		// the first complaint: errors.Join separates its entries with newlines, and
		// the screen draws one line per entry.
		joined := fmt.Errorf("%s 已移除，但切换器对它的部分记录没能清除。\n%w", name, errors.Join(errs...))
		log.Printf("remove: %v", joined)
		return dest, joined
	}
	return dest, nil
}
