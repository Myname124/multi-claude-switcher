// Package usage reads how much of its 5-hour and weekly limits a signed-in
// Claude account has used.
//
// The numbers come from the same endpoint Claude Code's /usage reads,
// api.anthropic.com/api/oauth/usage, called with the OAuth token Claude Desktop
// already keeps for the account in that profile's config.json. Nothing else is
// sent anywhere: the token goes to Anthropic, which issued it, and the reply is
// percentages and reset times.
//
// claude.ai has a twin of this endpoint that takes the sessionKey cookie, which
// is what browser-based usage meters call. It was not used here because
// claude.ai's API sits behind a Cloudflare bot check that challenges non-browser
// clients: measured from a Go/curl client, the first request returned JSON and
// every later one returned a "Just a moment..." HTML page. api.anthropic.com has
// no such check.
//
// The endpoint is not published by Anthropic, so the reply is parsed loosely:
// a missing window is reported as missing, never as zero.
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Endpoint is the OAuth usage endpoint Claude Code's /usage reads.
const Endpoint = "https://api.anthropic.com/api/oauth/usage"

// betaHeader is required for OAuth-authenticated calls to api.anthropic.com.
const betaHeader = "oauth-2025-04-20"

// Window is one rate-limit window: how much of it is used and when it resets.
type Window struct {
	// Percent is 0 to 100.
	Percent float64 `json:"percent"`
	// ResetsAt is zero when the server did not say, which it does for a window
	// nothing has been used in yet.
	ResetsAt time.Time `json:"resetsAt,omitempty"`
}

// Usage is what the endpoint reported for one account at one moment.
type Usage struct {
	FiveHour *Window `json:"fiveHour,omitempty"`
	SevenDay *Window `json:"sevenDay,omitempty"`
}

// Errors a caller can tell apart, because each needs a different sentence on
// screen.
var (
	// ErrNoToken: the profile holds no OAuth token, which means it is not signed
	// in, or Claude Desktop stores it in a way this reader does not know.
	ErrNoToken = errors.New("no sign-in token found in this profile")
	// ErrTokenExpired: every token found is past its expiry. Claude Desktop
	// renews it while it runs on the profile; this package never does, because
	// renewing rotates the refresh token and would sign Claude Desktop out.
	ErrTokenExpired = errors.New("the saved sign-in has expired; open this account in Claude once to renew it")
	// ErrUnauthorized: the server rejected the token.
	ErrUnauthorized = errors.New("Anthropic did not accept the saved sign-in; open this account in Claude once to renew it")
	// ErrForbidden: the token is valid but not allowed to read usage.
	ErrForbidden = errors.New("this account's sign-in is not allowed to read usage")
	// ErrUnsupported: no way to decrypt the token on this platform.
	ErrUnsupported = errors.New("reading usage is not supported on this platform")
)

// client is a var so tests can point it at a local server.
var client = &http.Client{Timeout: 15 * time.Second}

// endpoint is a var for the same reason.
var endpoint = Endpoint

// Fetch asks Anthropic for one account's usage. userAgent identifies this app.
func Fetch(token, userAgent string) (*Usage, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		// The error text from net/http can carry the URL but never the header,
		// so the token cannot leak through it.
		return nil, fmt.Errorf("could not reach Anthropic: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading the usage reply: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return parse(body)
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		return nil, ErrForbidden
	case http.StatusTooManyRequests:
		return nil, errors.New("Anthropic is rate-limiting usage checks; try again in a minute")
	default:
		return nil, fmt.Errorf("Anthropic returned %s", resp.Status)
	}
}

// rawWindow is one window as the endpoint sends it.
type rawWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

// parse reads the endpoint's reply. Only the two windows this app shows are
// kept; the reply also carries model-specific and add-on windows.
func parse(body []byte) (*Usage, error) {
	var r struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("the usage reply was not readable: %w", err)
	}
	u := &Usage{FiveHour: r.FiveHour.window(), SevenDay: r.SevenDay.window()}
	if u.FiveHour == nil && u.SevenDay == nil {
		return nil, errors.New("the usage reply had neither a 5-hour nor a weekly window")
	}
	return u, nil
}

func (w *rawWindow) window() *Window {
	if w == nil || w.Utilization == nil {
		return nil
	}
	out := &Window{Percent: clamp(*w.Utilization)}
	if w.ResetsAt != nil {
		out.ResetsAt = parseTime(*w.ResetsAt)
	}
	return out
}

func clamp(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// parseTime accepts RFC 3339 with or without fractional seconds, which covers
// every reset time seen from the endpoint. Anything else reads as "unknown".
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Effective returns the window as it stands at now. A window whose reset time
// has passed has started over, so it is reported as unused rather than at the
// percentage last read. This is what lets an account nobody has opened for a
// day show a correct 5-hour figure from an old reading.
func (w *Window) Effective(now time.Time) Window {
	if w == nil {
		return Window{}
	}
	if !w.ResetsAt.IsZero() && !now.Before(w.ResetsAt) {
		return Window{}
	}
	return *w
}
