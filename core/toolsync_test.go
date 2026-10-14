package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeDesktopConfig(t *testing.T, profile, body string) {
	t.Helper()
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, desktopConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDesktopConfigT(t *testing.T, profile string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(profile, desktopConfigName))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func serverNames(cfg map[string]any) []string {
	servers, _ := cfg["mcpServers"].(map[string]any)
	return sortedKeysAny(servers)
}

func sortedKeysAny(m map[string]any) []string {
	raw := map[string]json.RawMessage{}
	for k := range m {
		raw[k] = nil
	}
	return sortedKeys(raw)
}

func TestSyncMCPServersAddsMissingAndKeepsEverythingElse(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, src, `{"mcpServers":{"slicer":{"command":"slicer"},"ida":{"command":"ida"}}}`)
	writeDesktopConfig(t, dst, `{"preferences":{"sidebarMode":"x"},"mcpServers":{"own":{"command":"own"}}}`)

	r, err := SyncMCPServers(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ida", "slicer"}; !reflect.DeepEqual(r.Added, want) {
		t.Fatalf("Added = %v, want %v", r.Added, want)
	}
	cfg := readDesktopConfigT(t, dst)
	if got, want := serverNames(cfg), []string{"ida", "own", "slicer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
	if prefs, _ := cfg["preferences"].(map[string]any); prefs["sidebarMode"] != "x" {
		t.Fatalf("preferences lost: %v", cfg["preferences"])
	}
	if _, err := os.Stat(filepath.Join(dst, desktopConfigName+desktopConfigBackupSuffix)); err != nil {
		t.Fatal("no backup of the target's config was taken")
	}
}

func TestSyncMCPServersNeverOverwritesAConflict(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, src, `{"mcpServers":{"x":{"command":"theirs"}}}`)
	writeDesktopConfig(t, dst, `{"mcpServers":{"x":{"command":"mine"}}}`)

	r, err := SyncMCPServers(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 0 || !reflect.DeepEqual(r.Conflicts, []string{"x"}) {
		t.Fatalf("report = %+v, want just a conflict on x", r)
	}
	servers := readDesktopConfigT(t, dst)["mcpServers"].(map[string]any)
	if cmd := servers["x"].(map[string]any)["command"]; cmd != "mine" {
		t.Fatalf("target's server was overwritten: %v", cmd)
	}
}

func TestSyncMCPServersSameDefinitionIsNotAConflict(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, src, `{"mcpServers":{"x":{"command":"c","args":["a"]}}}`)
	writeDesktopConfig(t, dst, `{"mcpServers":{"x":{"args":["a"],  "command":"c"}}}`)
	r, err := SyncMCPServers(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 0 || len(r.Conflicts) != 0 {
		t.Fatalf("report = %+v, want nothing", r)
	}
}

func TestSyncMCPServersCreatesConfigForAFreshProfile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, src, `{"mcpServers":{"slicer":{"command":"slicer"}}}`)
	if _, err := SyncMCPServers(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := serverNames(readDesktopConfigT(t, dst)); !reflect.DeepEqual(got, []string{"slicer"}) {
		t.Fatalf("servers = %v", got)
	}
}

func TestSyncMCPServersLeavesAnUnreadableTargetAlone(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, src, `{"mcpServers":{"slicer":{"command":"slicer"}}}`)
	writeDesktopConfig(t, dst, `{not json`)
	if _, err := SyncMCPServers(src, dst); err == nil {
		t.Fatal("want an error for a config that does not parse")
	}
	raw, _ := os.ReadFile(filepath.Join(dst, desktopConfigName))
	if string(raw) != `{not json` {
		t.Fatalf("unreadable config was rewritten: %q", raw)
	}
}

func TestSyncMCPServersNothingToCopyWritesNothing(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, src, `{"mcpServers":{}}`)
	if _, err := SyncMCPServers(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, desktopConfigName)); err == nil {
		t.Fatal("a config was written with nothing to put in it")
	}
}

func TestSyncMCPServersBidirectionalUnions(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeDesktopConfig(t, a, `{"mcpServers":{"one":{"command":"1"}}}`)
	writeDesktopConfig(t, b, `{"mcpServers":{"two":{"command":"2"}}}`)
	if _, _, err := SyncMCPServersBidirectional(a, b); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{a, b} {
		if got := serverNames(readDesktopConfigT(t, p)); !reflect.DeepEqual(got, []string{"one", "two"}) {
			t.Fatalf("%s servers = %v", p, got)
		}
	}
}
