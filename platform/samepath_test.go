package platform

import (
	"path/filepath"
	"testing"
)

// TestSamePath pins the spellings the same profile directory actually arrives in:
// the platform reports a canonical path, a caller passes what the user typed.
func TestSamePath(t *testing.T) {
	base := filepath.Join("Users", "x", "Library", "Application Support", "Claude")

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", base, base, true},
		{"trailing separator", base, base + string(filepath.Separator), true},
		{"dot segment", base, filepath.Join(base, "."), true},
		{"different profiles", base, base + "_Profile2", false},
		{"one empty", base, "", false},
		{"both empty", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SamePath(tc.a, tc.b); got != tc.want {
				t.Errorf("SamePath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestSamePathCasing: Windows spells the same directory in whatever case it
// likes, so two spellings differing only in case are one directory.
func TestSamePathCasing(t *testing.T) {
	lower := filepath.Join("users", "x", "claude")
	upper := filepath.Join("Users", "X", "Claude")
	if !SamePath(lower, upper) {
		t.Errorf("SamePath(%q, %q) = false, want true", lower, upper)
	}
}
