package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		remote, local string
		want          bool
	}{
		{"v0.5.0", "0.4.0", true},
		{"0.5.0", "v0.5.0", false}, // equal (leading v ignored)
		{"v0.4.9", "0.5.0", false},
		{"v0.5.1", "0.5.0", true},
		{"v1.0.0", "0.9.9", true},
		{"v0.5.0", "0.5.0", false},
		{"v0.10.0", "0.9.0", true},     // numeric, not lexical
		{"v0.5.0-rc1", "0.5.0", false}, // pre-release suffix stripped -> equal
		{"v0.5.1-beta", "0.5.0", true}, // still newer on patch
		{"0.6", "0.5.0", true},         // short form
	}
	for _, c := range cases {
		if got := IsNewer(c.remote, c.local); got != c.want {
			t.Errorf("IsNewer(%q,%q)=%v want %v", c.remote, c.local, got, c.want)
		}
	}
}

func TestParseRelease(t *testing.T) {
	body := []byte(`{
		"tag_name": "v0.5.0",
		"assets": [
			{"name": "mcs-tray-macos-universal", "browser_download_url": "https://example.com/tray"},
			{"name": "multi-claude-switcher_0.5.0_macos-universal.zip", "browser_download_url": "https://example.com/zip"}
		]
	}`)
	tag, assets, err := parseRelease(body)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.5.0" {
		t.Errorf("tag = %q", tag)
	}
	if assets["mcs-tray-macos-universal"] != "https://example.com/tray" {
		t.Errorf("tray asset URL wrong: %q", assets["mcs-tray-macos-universal"])
	}
	if len(assets) != 2 {
		t.Errorf("expected 2 assets, got %d", len(assets))
	}
}

// fakeGitHub serves the three URLs LatestRelease uses and points them at itself.
func fakeGitHub(t *testing.T, api http.HandlerFunc, assetStatus int) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api", api)
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/owner/repo/releases/tag/v9.9.9", http.StatusFound)
	})
	mux.HandleFunc("/download/v9.9.9/"+WindowsInstallerName("v9.9.9"), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(assetStatus)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oldAPI, oldPage, oldDL := latestReleaseAPIURL, latestReleasePageURL, releaseDownloadBase
	latestReleaseAPIURL = srv.URL + "/api"
	latestReleasePageURL = srv.URL + "/releases/latest"
	releaseDownloadBase = srv.URL + "/download/"
	t.Cleanup(func() { latestReleaseAPIURL, latestReleasePageURL, releaseDownloadBase = oldAPI, oldPage, oldDL })
}

func rateLimited(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.Header().Set("X-RateLimit-Reset", "1791971183")
	w.WriteHeader(http.StatusForbidden)
}

func TestLatestReleaseUsesAPIWhenItWorks(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.3","assets":[{"name":"a","browser_download_url":"u"}]}`)
	}, http.StatusOK)
	tag, assets, err := LatestRelease()
	if err != nil || tag != "v1.2.3" || assets["a"] != "u" {
		t.Fatalf("got %q %v %v", tag, assets, err)
	}
}

func TestLatestReleaseFallsBackToPageWhenRateLimited(t *testing.T) {
	fakeGitHub(t, rateLimited, http.StatusOK)
	tag, assets, err := LatestRelease()
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v9.9.9" {
		t.Fatalf("tag = %q, want v9.9.9", tag)
	}
	name := WindowsInstallerName(tag)
	if want := releaseDownloadBase + "v9.9.9/" + name; assets[name] != want {
		t.Fatalf("assets = %v, want %s -> %s", assets, name, want)
	}
}

func TestLatestReleasePageWithoutInstallerHasNoAssets(t *testing.T) {
	fakeGitHub(t, rateLimited, http.StatusNotFound)
	tag, assets, err := LatestRelease()
	if err != nil || tag != "v9.9.9" || len(assets) != 0 {
		t.Fatalf("got %q %v %v, want the tag and no assets", tag, assets, err)
	}
}

func TestLatestReleaseExplainsRateLimitWhenBothFail(t *testing.T) {
	fakeGitHub(t, rateLimited, http.StatusOK)
	latestReleasePageURL = "http://127.0.0.1:1/unreachable"
	_, _, err := LatestRelease()
	if err == nil || !strings.Contains(err.Error(), "访问次数用完了") {
		t.Fatalf("err = %v, want it to name the spent quota", err)
	}
}

func TestWindowsInstallerName(t *testing.T) {
	if got := WindowsInstallerName("v0.14.2"); got != "Multi-Claude-Switcher_0.14.2_windows_setup.exe" {
		t.Fatalf("got %q", got)
	}
}
