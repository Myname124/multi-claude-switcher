package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// tokenCacheKeyPrefix marks the config.json entries holding Claude Desktop's
// OAuth tokens: "oauth:tokenCache" and, on newer builds, "oauth:tokenCacheV2".
const tokenCacheKeyPrefix = "oauth:tokenCache"

// accessTokenPattern is the shape of an Anthropic OAuth access token. Refresh
// tokens are "sk-ant-ort" and are never used here.
var accessTokenPattern = regexp.MustCompile(`sk-ant-oat[0-9]{2}-[A-Za-z0-9_\-]+`)

// candidate is one access token found in a profile.
type candidate struct {
	token string
	// expires is zero when the cache did not say.
	expires time.Time
	// profileScope is true when the entry the token came from names the
	// user:profile scope, which the usage endpoint requires.
	profileScope bool
}

// readCandidates returns the access tokens a profile holds, best first: those
// known to carry user:profile, then the latest expiry, then unknown expiry.
// Expired tokens are dropped; if every token found has expired the error is
// ErrTokenExpired.
func readCandidates(profilePath string, dec decryptor, now time.Time) ([]candidate, error) {
	data, err := os.ReadFile(filepath.Join(profilePath, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read config.json: %w", err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}
	// Newest format first, so a V2 token wins over a stale V1 one.
	var keys []string
	for k := range cfg {
		if strings.HasPrefix(k, tokenCacheKeyPrefix) {
			keys = append(keys, k)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))

	var all []candidate
	decryptFailed := 0
	for _, k := range keys {
		for _, blob := range storedBlobs(cfg[k]) {
			plain, err := dec(blob)
			if err != nil {
				decryptFailed++
				continue
			}
			all = append(all, extractCandidates(plain)...)
		}
	}
	if len(all) == 0 {
		if decryptFailed > 0 {
			return nil, errKey(fmt.Errorf("%d saved value(s) did not decrypt", decryptFailed))
		}
		return nil, ErrNoToken
	}

	seen := map[string]bool{}
	var live []candidate
	for _, c := range all {
		if seen[c.token] {
			continue
		}
		seen[c.token] = true
		// A minute of margin: a token about to lapse would only fail in flight.
		if !c.expires.IsZero() && !c.expires.After(now.Add(time.Minute)) {
			continue
		}
		live = append(live, c)
	}
	if len(live) == 0 {
		return nil, ErrTokenExpired
	}
	sort.SliceStable(live, func(i, j int) bool {
		a, b := live[i], live[j]
		if a.profileScope != b.profileScope {
			return a.profileScope
		}
		return a.expires.After(b.expires)
	})
	return live, nil
}

// extractCandidates finds access tokens in decrypted token-cache plaintext.
//
// The plaintext's layout is Claude Desktop's own and undocumented, so it is
// read by shape: any string that looks like an access token is one, and an
// expiry or scope list sitting in the same JSON object is attributed to it.
// Plaintext that is not JSON still yields its tokens, without expiry.
func extractCandidates(plain []byte) []candidate {
	var v any
	if err := json.Unmarshal(plain, &v); err != nil {
		var out []candidate
		for _, t := range accessTokenPattern.FindAllString(string(plain), -1) {
			out = append(out, candidate{token: t})
		}
		return out
	}
	var out []candidate
	var walk func(v any, keyHint string, depth int)
	walk = func(v any, keyHint string, depth int) {
		if depth > 8 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			var tokens []string
			for _, e := range t {
				if s, ok := e.(string); ok && accessTokenPattern.MatchString(s) {
					tokens = append(tokens, accessTokenPattern.FindString(s))
				}
			}
			if len(tokens) > 0 {
				exp := expiryIn(t)
				scope := strings.Contains(keyHint, "user:profile") || scopeIn(t)
				for _, tok := range tokens {
					out = append(out, candidate{token: tok, expires: exp, profileScope: scope})
				}
			}
			for k, e := range t {
				switch e.(type) {
				case map[string]any, []any:
					walk(e, k, depth+1)
				}
			}
		case []any:
			for _, e := range t {
				walk(e, keyHint, depth+1)
			}
		case string:
			if accessTokenPattern.MatchString(t) {
				out = append(out, candidate{token: accessTokenPattern.FindString(t), profileScope: strings.Contains(keyHint, "user:profile")})
			}
		}
	}
	walk(v, "", 0)
	return out
}

// expiryIn reads an expiry from the object holding a token. Seconds and
// milliseconds since the epoch and RFC 3339 strings are all accepted, under the
// field names OAuth clients commonly use.
func expiryIn(m map[string]any) time.Time {
	for _, k := range []string{"expiresAt", "expires_at", "expiry", "expires", "expiration"} {
		switch v := m[k].(type) {
		case float64:
			if v > 1e12 {
				return time.UnixMilli(int64(v))
			}
			if v > 1e9 {
				return time.Unix(int64(v), 0)
			}
		case string:
			if t := parseTime(v); !t.IsZero() {
				return t
			}
		}
	}
	return time.Time{}
}

// scopeIn reports whether the object lists the user:profile scope, as a
// string or an array of strings.
func scopeIn(m map[string]any) bool {
	for _, k := range []string{"scope", "scopes"} {
		switch v := m[k].(type) {
		case string:
			if strings.Contains(v, "user:profile") {
				return true
			}
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok && s == "user:profile" {
					return true
				}
			}
		}
	}
	return false
}
