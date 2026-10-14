package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// LatestReleaseURL is the GitHub API endpoint for this project's latest release.
const LatestReleaseURL = "https://api.github.com/repos/Myname124/multi-claude-switcher/releases/latest"

// IsNewer reports whether the remote version string is strictly newer than the
// local one. Both may carry a leading "v" and a pre-release/build suffix
// (compared on major.minor.patch only).
func IsNewer(remote, local string) bool {
	return compareVersions(remote, local) > 0
}

func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseVersion(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i] // drop pre-release / build metadata
	}
	var out [3]int
	for i, part := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		out[i], _ = strconv.Atoi(part)
	}
	return out
}

// parseRelease extracts the tag and an asset-name -> download-URL map from a
// GitHub "latest release" API response body.
func parseRelease(data []byte) (tag string, assets map[string]string, err error) {
	var r struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", nil, err
	}
	m := make(map[string]string, len(r.Assets))
	for _, a := range r.Assets {
		m[a.Name] = a.URL
	}
	return r.TagName, m, nil
}

// LatestRelease fetches the latest release tag and its assets from GitHub.
//
// The API answers anonymous callers 60 times an hour per IP, and a shared or
// proxied address can have spent that before this app asks; the check then
// failed with a bare "403 Forbidden". The release web page has no such quota,
// so when the API fails the page is asked instead.
func LatestRelease() (tag string, assets map[string]string, err error) {
	tag, assets, apiErr := latestReleaseFromAPI()
	if apiErr == nil {
		return tag, assets, nil
	}
	tag, assets, pageErr := latestReleaseFromPage()
	if pageErr == nil {
		log.Printf("Update check: GitHub API unavailable (%v); used the release page instead", apiErr)
		return tag, assets, nil
	}
	return "", nil, fmt.Errorf("%v\n备用地址也连不上：%v", apiErr, pageErr)
}

// These are variables so tests can point them at a local server.
var (
	latestReleaseAPIURL  = LatestReleaseURL
	latestReleasePageURL = "https://github.com/Myname124/multi-claude-switcher/releases/latest"
	releaseDownloadBase  = "https://github.com/Myname124/multi-claude-switcher/releases/download/"
)

func latestReleaseFromAPI() (tag string, assets map[string]string, err error) {
	req, err := http.NewRequest(http.MethodGet, latestReleaseAPIURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "multi-claude-switcher")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, apiStatusError(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	return parseRelease(data)
}

// apiStatusError says what a failed API call means. A spent quota is the
// common case and the one a user can act on, so it names when it resets.
func apiStatusError(resp *http.Response) error {
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
		resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return fmt.Errorf("GitHub 接口这个小时的访问次数用完了，%s 后恢复", time.Unix(reset, 0).Format("15:04"))
		}
		return fmt.Errorf("GitHub 接口这个小时的访问次数用完了")
	}
	return fmt.Errorf("github api returned %s", resp.Status)
}

// latestReleaseFromPage reads the newest tag from where the release page
// redirects, then confirms the Windows installer is attached. A release whose
// installer has not been uploaded yet comes back with no assets, which the
// caller already reports as "no download for this computer yet".
func latestReleaseFromPage() (tag string, assets map[string]string, err error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, latestReleasePageURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "multi-claude-switcher")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	resp.Body.Close()
	tag, err = tagFromReleaseRedirect(resp)
	if err != nil {
		return "", nil, err
	}

	name := WindowsInstallerName(tag)
	url := releaseDownloadBase + tag + "/" + name
	head, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return "", nil, err
	}
	head.Header.Set("User-Agent", "multi-claude-switcher")
	hresp, err := (&http.Client{Timeout: 15 * time.Second}).Do(head)
	if err != nil {
		return "", nil, err
	}
	hresp.Body.Close()
	switch hresp.StatusCode {
	case http.StatusOK:
		return tag, map[string]string{name: url}, nil
	case http.StatusNotFound:
		return tag, map[string]string{}, nil
	default:
		return "", nil, fmt.Errorf("checking %s returned %s", name, hresp.Status)
	}
}

func tagFromReleaseRedirect(resp *http.Response) (string, error) {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("release page returned %s", resp.Status)
	}
	loc := resp.Header.Get("Location")
	const marker = "/releases/tag/"
	i := strings.LastIndex(loc, marker)
	if i < 0 {
		// No release at all redirects to the list of releases instead.
		return "", fmt.Errorf("还没有发布过版本")
	}
	tag := strings.Trim(loc[i+len(marker):], "/")
	if tag == "" || strings.ContainsAny(tag, "/?#") {
		return "", fmt.Errorf("unexpected release redirect %q", loc)
	}
	return tag, nil
}

// WindowsInstallerName is the release asset the Windows updater installs, as
// the release workflow names it.
func WindowsInstallerName(tag string) string {
	return "Multi-Claude-Switcher_" + strings.TrimPrefix(tag, "v") + "_windows_setup.exe"
}

// DownloadTo streams a URL to a destination file (truncating it).
func DownloadTo(url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "multi-claude-switcher")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return f.Close()
}
