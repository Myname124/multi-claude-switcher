package usage

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/miou1107/multi-claude-switcher/core"
)

// Target is one signed-in account to read usage for.
type Target struct {
	// AccountUUID keys the cache, so a renamed profile keeps its reading.
	AccountUUID string
	ProfilePath string
}

// Entry is the latest reading for one account.
type Entry struct {
	// Usage is the last successful reading, kept across failures so an account
	// whose token has lapsed still shows what it last reported.
	Usage     *Usage    `json:"usage,omitempty"`
	FetchedAt time.Time `json:"fetchedAt,omitempty"`
	// Err is why the most recent attempt failed, empty when it succeeded. It is
	// a sentence for the panel, never the underlying error with its details.
	Err string `json:"-"`
}

// minInterval keeps opening and closing the panel from turning into a stream
// of requests: a refresh within this long of the previous one is skipped.
const minInterval = 60 * time.Second

// Store caches readings in memory and on disk. Only successful readings are
// written to disk: they are percentages and times, nothing secret, and
// keeping them is what lets an account show its last reading after a restart.
type Store struct {
	mu       sync.Mutex
	entries  map[string]Entry
	loaded   bool
	running  bool
	lastRun  time.Time
	path     func() string
	readOne  func(profilePath string) (*Usage, error)
	now      func() time.Time
	interval time.Duration
}

// Default is the store both hosts share.
var Default = &Store{
	path: func() string {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".multi-claude-switcher", "usage.json")
	},
	readOne:  ReadProfile,
	now:      time.Now,
	interval: minInterval,
}

// Lookup returns the cached entry for an account.
func (s *Store) Lookup(accountUUID string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	e, ok := s.entries[accountUUID]
	return e, ok
}

// Refresh reads every target in the background and calls done once when it
// has finished, if anything was read. It returns at once. A refresh already
// running, or one finished less than a minute ago, makes this a no-op, so
// callers can invoke it every time the panel opens.
func (s *Store) Refresh(targets []Target, done func()) {
	s.mu.Lock()
	s.loadLocked()
	if s.running || len(targets) == 0 || (!s.lastRun.IsZero() && s.now().Sub(s.lastRun) < s.interval) {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		for _, t := range targets {
			u, err := s.readOne(t.ProfilePath)
			s.mu.Lock()
			e := s.entries[t.AccountUUID]
			if err != nil {
				log.Printf("usage: %s: %v", filepath.Base(t.ProfilePath), err)
				e.Err = panelReason(err)
			} else {
				e = Entry{Usage: u, FetchedAt: s.now()}
			}
			s.entries[t.AccountUUID] = e
			s.mu.Unlock()
		}
		s.mu.Lock()
		s.running = false
		s.lastRun = s.now()
		s.saveLocked()
		s.mu.Unlock()
		if done != nil {
			done()
		}
	}()
}

// panelReason is the short sentence the panel shows for a failed read.
func panelReason(err error) string {
	switch {
	case errors.Is(err, ErrTokenExpired), errors.Is(err, ErrUnauthorized):
		return "在 Claude 里打开一次这个账号即可刷新额度。"
	case errors.Is(err, ErrNoToken):
		return "没有可用来读取额度的登录信息。"
	case errors.Is(err, ErrForbidden):
		return "这个登录无权读取额度。"
	case errors.Is(err, ErrUnsupported):
		return "这个系统上无法读取额度。"
	default:
		return "暂时读不到额度。"
	}
}

func (s *Store) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.entries = map[string]Entry{}
	p := s.path()
	if p == "" {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &s.entries)
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
}

func (s *Store) saveLocked() {
	p := s.path()
	if p == "" {
		return
	}
	keep := map[string]Entry{}
	for k, e := range s.entries {
		if e.Usage != nil {
			keep[k] = Entry{Usage: e.Usage, FetchedAt: e.FetchedAt}
		}
	}
	data, err := json.MarshalIndent(keep, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// maxAttempts bounds how many tokens one profile may try. A profile normally
// holds one; trying every token a damaged cache might list is not useful.
const maxAttempts = 3

// ReadProfile reads usage for the account signed in to one profile.
func ReadProfile(profilePath string) (*Usage, error) {
	dec, err := newDecryptor(profilePath)
	if err != nil {
		return nil, err
	}
	cands, err := readCandidates(profilePath, dec, time.Now())
	if err != nil {
		return nil, err
	}
	ua := "multi-claude-switcher/" + core.Version
	var last error
	for i, c := range cands {
		if i == maxAttempts {
			break
		}
		u, err := Fetch(c.token, ua)
		if err == nil {
			return u, nil
		}
		last = err
		// Only a rejected token is worth trying the next one for. Anything else
		// (network, rate limit) would fail the same way again.
		if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
			break
		}
	}
	return nil, last
}
