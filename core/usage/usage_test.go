package usage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPBKDF2MatchesRFC6070(t *testing.T) {
	for _, tc := range []struct {
		iter int
		want string
	}{
		{1, "0c60c80f961f0e71f3a9b524af6012062fe037a6"},
		{2, "ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957"},
		{4096, "4b007901b765489abead49d926f721d065a429c1"},
	} {
		got := hex.EncodeToString(pbkdf2SHA1([]byte("password"), []byte("salt"), tc.iter, 20))
		if got != tc.want {
			t.Errorf("iter %d: got %s want %s", tc.iter, got, tc.want)
		}
	}
}

// encryptCBC is the macOS scheme run forwards, for fixtures.
func encryptCBC(t *testing.T, key, plain []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	n := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(n)}, n)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(out, padded)
	return append([]byte("v10"), out...)
}

// encryptGCM is the Windows scheme run forwards, for fixtures.
func encryptGCM(t *testing.T, key, plain []byte) []byte {
	t.Helper()
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := []byte("0123456789ab")
	return append(append([]byte("v10"), nonce...), gcm.Seal(nil, nonce, plain, nil)...)
}

func TestCBCRoundTripAndWrongKey(t *testing.T) {
	key := macKey([]byte("keychain-password"))
	blob := encryptCBC(t, key, []byte(`{"hello":"world"}`))
	got, err := decryptCBC(key, blob)
	if err != nil || string(got) != `{"hello":"world"}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := decryptCBC(macKey([]byte("other")), blob); err == nil {
		t.Fatal("a wrong key decrypted without error")
	}
}

func TestGCMRoundTripAndTamper(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	blob := encryptGCM(t, key, []byte("secret"))
	got, err := decryptGCM(key, blob)
	if err != nil || string(got) != "secret" {
		t.Fatalf("got %q, %v", got, err)
	}
	blob[len(blob)-1] ^= 1
	if _, err := decryptGCM(key, blob); err == nil {
		t.Fatal("a tampered value decrypted without error")
	}
}

func TestStoredBlobsAcceptsEveryEncoding(t *testing.T) {
	blob := append([]byte("v10"), []byte{1, 2, 3, 4}...)
	nums := make([]int, len(blob))
	for i, b := range blob {
		nums[i] = int(b)
	}
	numsJSON, _ := json.Marshal(nums)
	cases := map[string]string{
		"base64":      `"` + base64.StdEncoding.EncodeToString(blob) + `"`,
		"hex":         `"` + hex.EncodeToString(blob) + `"`,
		"buffer":      `{"type":"Buffer","data":` + string(numsJSON) + `}`,
		"byte array":  string(numsJSON),
		"nested":      `{"a":{"b":"` + base64.StdEncoding.EncodeToString(blob) + `"}}`,
		"inside list": `["` + base64.StdEncoding.EncodeToString(blob) + `"]`,
	}
	for name, raw := range cases {
		got := storedBlobs(json.RawMessage(raw))
		if len(got) != 1 || !bytes.Equal(got[0], blob) {
			t.Errorf("%s: got %v", name, got)
		}
	}
	// A string that decodes but is not a safeStorage value is not a secret.
	if got := storedBlobs(json.RawMessage(`"aGVsbG8="`)); len(got) != 0 {
		t.Errorf("plain base64 was taken for a blob: %v", got)
	}
}

const tokA = "sk-ant-oat01-AAAAaaaa_1111-xyz"
const tokB = "sk-ant-oat01-BBBBbbbb_2222-xyz"

func TestExtractCandidatesReadsByShape(t *testing.T) {
	plain := fmt.Sprintf(`{
		"clientA:acct:user:inference": {"token": %q, "refreshToken": "sk-ant-ort01-nope", "expiresAt": 1900000000000},
		"clientB:acct:user:inference user:profile": {"accessToken": %q, "expires_at": "2030-01-01T00:00:00Z"}
	}`, tokA, tokB)
	got := extractCandidates([]byte(plain))
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %+v", got)
	}
	byTok := map[string]candidate{}
	for _, c := range got {
		byTok[c.token] = c
	}
	if c := byTok[tokA]; c.expires.UnixMilli() != 1900000000000 || c.profileScope {
		t.Errorf("token A read as %+v", c)
	}
	if c := byTok[tokB]; c.expires.Year() != 2030 || !c.profileScope {
		t.Errorf("token B read as %+v", c)
	}
	for _, c := range got {
		if strings.HasPrefix(c.token, "sk-ant-ort") {
			t.Error("a refresh token was offered as an access token")
		}
	}
	// Not JSON at all: the token is still found.
	if got := extractCandidates([]byte("junk " + tokA + " junk")); len(got) != 1 || got[0].token != tokA {
		t.Errorf("plaintext token not found: %+v", got)
	}
}

// writeProfile makes a profile whose config.json holds plain encrypted with
// the identity "cipher" below (prefix v10, plaintext appended).
func writeProfile(t *testing.T, key string, plain string) string {
	t.Helper()
	dir := t.TempDir()
	blob := base64.StdEncoding.EncodeToString(append([]byte("v10"), plain...))
	cfg := map[string]any{"lastKnownAccountUuid": "u", key: blob}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func identity(blob []byte) ([]byte, error) { return blob[3:], nil }

func TestReadCandidatesOrderAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	plain := fmt.Sprintf(`{"x":{"token":%q,"expiresAt":%d},"y user:profile":{"token":%q,"expiresAt":%d}}`,
		tokA, now.Add(5*time.Hour).UnixMilli(), tokB, now.Add(time.Hour).UnixMilli())
	dir := writeProfile(t, "oauth:tokenCacheV2", plain)
	got, err := readCandidates(dir, identity, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].token != tokB {
		t.Fatalf("the user:profile token should come first: %+v", got)
	}

	expired := fmt.Sprintf(`{"x":{"token":%q,"expiresAt":%d}}`, tokA, now.Add(-time.Hour).UnixMilli())
	dir = writeProfile(t, "oauth:tokenCache", expired)
	if _, err := readCandidates(dir, identity, now); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}

	dir = writeProfile(t, "somethingElse", plain)
	if _, err := readCandidates(dir, identity, now); !errors.Is(err, ErrNoToken) {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestFetchParsesAndMapsStatuses(t *testing.T) {
	var gotAuth, gotBeta string
	status := http.StatusOK
	body := `{"five_hour":{"utilization":42.5,"resets_at":"2026-10-14T15:00:00.123456+00:00"},
	          "seven_day":{"utilization":18,"resets_at":null},"seven_day_opus":null}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL
	defer func() { endpoint = old }()

	u, err := Fetch(tokA, "test")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+tokA || gotBeta != betaHeader {
		t.Errorf("headers: %q %q", gotAuth, gotBeta)
	}
	if u.FiveHour == nil || u.FiveHour.Percent != 42.5 || u.FiveHour.ResetsAt.Hour() != 15 {
		t.Errorf("five hour: %+v", u.FiveHour)
	}
	if u.SevenDay == nil || u.SevenDay.Percent != 18 || !u.SevenDay.ResetsAt.IsZero() {
		t.Errorf("seven day: %+v", u.SevenDay)
	}

	for st, want := range map[int]error{401: ErrUnauthorized, 403: ErrForbidden} {
		status = st
		if _, err := Fetch(tokA, "test"); !errors.Is(err, want) {
			t.Errorf("status %d: got %v", st, err)
		}
	}
	status, body = 200, `{"other":1}`
	if _, err := Fetch(tokA, "test"); err == nil {
		t.Error("a reply with no windows was accepted")
	}
}

func TestEffectiveResetsAfterResetTime(t *testing.T) {
	reset := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	w := &Window{Percent: 80, ResetsAt: reset}
	if got := w.Effective(reset.Add(-time.Minute)); got.Percent != 80 {
		t.Errorf("before reset: %+v", got)
	}
	if got := w.Effective(reset); got.Percent != 0 {
		t.Errorf("at reset: %+v", got)
	}
	if got := (*Window)(nil).Effective(reset); got.Percent != 0 {
		t.Errorf("nil: %+v", got)
	}
}

func TestStoreRefreshKeepsLastGoodReadingAndPersists(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	fail := false
	s := &Store{
		path: func() string { return filepath.Join(dir, "usage.json") },
		readOne: func(string) (*Usage, error) {
			mu.Lock()
			defer mu.Unlock()
			if fail {
				return nil, ErrTokenExpired
			}
			return &Usage{FiveHour: &Window{Percent: 10}}, nil
		},
		now:      func() time.Time { return now },
		interval: time.Minute,
	}
	targets := []Target{{AccountUUID: "acct", ProfilePath: "/p"}}
	wait := func() {
		done := make(chan struct{})
		s.Refresh(targets, func() { close(done) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("refresh never finished")
		}
	}
	wait()
	e, ok := s.Lookup("acct")
	if !ok || e.Usage == nil || e.Usage.FiveHour.Percent != 10 || e.Err != "" {
		t.Fatalf("after success: %+v", e)
	}

	// Within the interval nothing runs, and done is not called.
	called := false
	s.Refresh(targets, func() { called = true })
	time.Sleep(50 * time.Millisecond)
	if called {
		t.Fatal("a refresh inside the interval ran")
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	now = now.Add(2 * time.Minute)
	wait()
	e, _ = s.Lookup("acct")
	if e.Usage == nil || e.Err == "" {
		t.Fatalf("a failure should keep the old reading and say why: %+v", e)
	}

	// A fresh store reads the last good reading back from disk, without the
	// transient error.
	s2 := &Store{path: s.path, readOne: s.readOne, now: s.now, interval: time.Minute}
	e, ok = s2.Lookup("acct")
	if !ok || e.Usage == nil || e.Usage.FiveHour.Percent != 10 || e.Err != "" {
		t.Fatalf("reloaded: %+v", e)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "usage.json"))
	if strings.Contains(string(data), "sk-ant") {
		t.Fatal("the cache file holds a token")
	}
}
