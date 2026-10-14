package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// desktopConfigName is Claude Desktop's own settings file inside a profile. Its
// mcpServers object holds the local MCP servers the user added by hand, and it
// is the one piece of tool setup that lives per profile. Claude Code's skills,
// plugins and MCP servers live under ~/.claude and are already shared by every
// profile; skills and connectors added on claude.ai live with the account on
// Anthropic's side and cannot be copied from here.
const desktopConfigName = "claude_desktop_config.json"

// desktopConfigBackupSuffix names the copy of the target's config taken before
// it is rewritten. One per profile, replaced on each write: a merge only ever
// adds servers, so the previous version is all that is needed to undo one.
const desktopConfigBackupSuffix = ".mcs-bak"

// ToolSyncReport says what SyncMCPServers did.
type ToolSyncReport struct {
	// Added are the server names copied into the target.
	Added []string
	// Conflicts are names both profiles define differently. The target keeps
	// its own; nothing is overwritten.
	Conflicts []string
}

// SyncMCPServers copies the MCP servers defined in src's Claude Desktop config
// into dst's, by name. A name dst already has is left alone, so this never
// removes or changes anything dst had; a definition that differs is reported as
// a conflict. Every other key in dst's config is kept as it was. Claude Desktop
// must be closed, since it rewrites this file while it runs.
func SyncMCPServers(srcProfilePath, dstProfilePath string) (*ToolSyncReport, error) {
	report := &ToolSyncReport{}
	src, _, err := readDesktopConfig(srcProfilePath)
	if err != nil {
		return nil, err
	}
	srcServers, err := mcpServersOf(src, srcProfilePath)
	if err != nil {
		return nil, err
	}
	if len(srcServers) == 0 {
		return report, nil
	}
	dst, dstRaw, err := readDesktopConfig(dstProfilePath)
	if err != nil {
		return nil, err
	}
	dstServers, err := mcpServersOf(dst, dstProfilePath)
	if err != nil {
		return nil, err
	}
	if dstServers == nil {
		dstServers = map[string]json.RawMessage{}
	}

	for _, name := range sortedKeys(srcServers) {
		def := srcServers[name]
		existing, ok := dstServers[name]
		if !ok {
			dstServers[name] = def
			report.Added = append(report.Added, name)
			continue
		}
		if !jsonEqual(existing, def) {
			report.Conflicts = append(report.Conflicts, name)
		}
	}
	if len(report.Added) == 0 {
		return report, nil
	}

	servers, err := json.Marshal(dstServers)
	if err != nil {
		return nil, err
	}
	if dst == nil {
		dst = map[string]json.RawMessage{}
	}
	dst["mcpServers"] = servers
	out, err := json.MarshalIndent(dst, "", "  ")
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dstProfilePath, desktopConfigName)
	if dstRaw != nil {
		if err := os.WriteFile(path+desktopConfigBackupSuffix, dstRaw, 0o644); err != nil {
			return nil, fmt.Errorf("没有改动 %s 的 MCP 配置：备份失败（%w）", DisplayName(filepath.Base(dstProfilePath)), err)
		}
	}
	if err := writeFileAtomic(path, out); err != nil {
		return nil, err
	}
	return report, nil
}

// SyncMCPServersBidirectional unions both profiles' MCP servers.
func SyncMCPServersBidirectional(a, b string) (aToB, bToA *ToolSyncReport, err error) {
	if aToB, err = SyncMCPServers(a, b); err != nil {
		return nil, nil, err
	}
	if bToA, err = SyncMCPServers(b, a); err != nil {
		return aToB, nil, err
	}
	return aToB, bToA, nil
}

// readDesktopConfig returns a profile's config decoded one level deep, plus the
// bytes it was read from. A missing file is not an error: a profile that never
// had a server added has none, and gets one written when servers are copied in.
// A file that does not parse is an error, so it is never replaced by a guess.
func readDesktopConfig(profilePath string) (map[string]json.RawMessage, []byte, error) {
	path := filepath.Join(profilePath, desktopConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var cfg map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, nil, fmt.Errorf("%s 的 %s 无法解析，没有改动它（%w）", DisplayName(filepath.Base(profilePath)), desktopConfigName, err)
		}
	}
	return cfg, raw, nil
}

func mcpServersOf(cfg map[string]json.RawMessage, profilePath string) (map[string]json.RawMessage, error) {
	raw, ok := cfg["mcpServers"]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("%s 的 mcpServers 格式不对，没有改动它（%w）", DisplayName(filepath.Base(profilePath)), err)
	}
	return servers, nil
}

// jsonEqual compares two JSON values by meaning, so key order and whitespace
// do not count as a conflict.
func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return bytes.Equal(a, b)
	}
	ca, _ := json.Marshal(va)
	cb, _ := json.Marshal(vb)
	return bytes.Equal(ca, cb)
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeFileAtomic writes through a temp file in the same directory, so a crash
// mid-write leaves the old config rather than half of a new one.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
