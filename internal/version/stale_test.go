package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShortCommit(t *testing.T) {
	tests := []struct {
		name   string
		hash   string
		expect string
	}{
		{"full SHA", "abcdef1234567890abcdef1234567890abcdef12", "abcdef123456"},
		{"exactly 12", "abcdef123456", "abcdef123456"},
		{"short hash", "abcdef", "abcdef"},
		{"empty", "", ""},
		{"13 chars", "abcdef1234567", "abcdef123456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortCommit(tt.hash)
			if got != tt.expect {
				t.Errorf("ShortCommit(%q) = %q, want %q", tt.hash, got, tt.expect)
			}
		})
	}
}

func TestCommitsMatch(t *testing.T) {
	tests := []struct {
		name   string
		a, b   string
		expect bool
	}{
		{"identical full", "abcdef1234567890", "abcdef1234567890", true},
		{"prefix match short-long", "abcdef1234567", "abcdef1234567890abcd", true},
		{"prefix match long-short", "abcdef1234567890abcd", "abcdef1234567", true},
		{"no match", "abcdef1234567", "1234567abcdef", false},
		{"too short a", "abc", "abcdef1234567", false},
		{"too short b", "abcdef1234567", "abc", false},
		{"both too short", "abc", "abc", false},
		{"exactly 7 chars match", "abcdefg", "abcdefg", true},
		{"exactly 7 chars no match", "abcdefg", "abcdefh", false},
		{"6 chars too short", "abcdef", "abcdef", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commitsMatch(tt.a, tt.b)
			if got != tt.expect {
				t.Errorf("commitsMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expect)
			}
		})
	}
}

func TestSetCommit(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	SetCommit("abc123def456")
	if Commit != "abc123def456" {
		t.Errorf("SetCommit did not set Commit; got %q", Commit)
	}
}

func TestCheckStaleBinary_NoCommit(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = ""
	// Force resolveCommitHash to return empty by clearing Commit
	// (vcs.revision from build info may still be set, so this test
	// verifies the error path when no commit is available)
	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("CheckStaleBinary returned nil")
	}
	// Either we get an error (no commit) or we get a valid result from build info
	// Both are acceptable outcomes
	if info.BinaryCommit == "" && info.Error == nil {
		t.Error("expected error when binary commit is empty")
	}
}

func TestIsHexCommit(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"full SHA", "abcdef1234567890abcdef1234567890abcdef12", true},
		{"short SHA", "abcdef1", true},
		{"uppercase", "ABCDEF1234567", true},
		{"mixed case", "aBcDeF1234567", true},
		{"too short", "abcdef", false},
		{"empty", "", false},
		{"Homebrew", "Homebrew", false},
		{"with spaces", "abcdef 123456", false},
		{"with non-hex", "ghijkl1234567", false},
		{"exactly 7", "abcdef1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHexCommit(tt.input)
			if got != tt.expect {
				t.Errorf("isHexCommit(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestCheckStaleBinary_HomebrewPath(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	// Set commit to "Homebrew" to simulate Homebrew-built binary
	Commit = "Homebrew"

	// Stub brew commands to simulate current install
	origGetVersion := getBrewInstalledVersion
	origCheckOutdated := checkBrewOutdated
	defer func() {
		getBrewInstalledVersion = origGetVersion
		checkBrewOutdated = origCheckOutdated
	}()

	getBrewInstalledVersion = func() string { return "1.2.3" }
	checkBrewOutdated = func() (bool, error) { return false, nil }

	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if info.InstallMethod != InstallMethodHomebrew {
		t.Errorf("InstallMethod = %q, want %q", info.InstallMethod, InstallMethodHomebrew)
	}
	if info.IsStale {
		t.Error("expected IsStale=false for current Homebrew install")
	}
}

func TestCheckStaleBinary_HomebrewOutdated(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = "Homebrew"

	origGetVersion := getBrewInstalledVersion
	origCheckOutdated := checkBrewOutdated
	defer func() {
		getBrewInstalledVersion = origGetVersion
		checkBrewOutdated = origCheckOutdated
	}()

	getBrewInstalledVersion = func() string { return "1.2.3" }
	checkBrewOutdated = func() (bool, error) { return true, nil }

	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if info.InstallMethod != InstallMethodHomebrew {
		t.Errorf("InstallMethod = %q, want %q", info.InstallMethod, InstallMethodHomebrew)
	}
	if !info.IsStale {
		t.Error("expected IsStale=true for outdated Homebrew install")
	}
}

func TestCheckStaleBinary_HomebrewNotInstalled(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = "Homebrew"

	origGetVersion := getBrewInstalledVersion
	origCheckOutdated := checkBrewOutdated
	defer func() {
		getBrewInstalledVersion = origGetVersion
		checkBrewOutdated = origCheckOutdated
	}()

	// Simulate brew not finding gt
	getBrewInstalledVersion = func() string { return "" }
	checkBrewOutdated = func() (bool, error) { return false, nil }

	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if info.IsStale {
		t.Error("expected IsStale=false when brew can't determine version")
	}
}

func TestBrewCacheHit(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = "Homebrew"

	origGetVersion := getBrewInstalledVersion
	origCheckOutdated := checkBrewOutdated
	origCachePath := brewStaleCachePath
	defer func() {
		getBrewInstalledVersion = origGetVersion
		checkBrewOutdated = origCheckOutdated
		brewStaleCachePath = origCachePath
	}()

	// Set up a valid cache file
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "brew-stale-cache.json")
	entry := brewStaleCacheEntry{
		InstalledVersion: "1.2.3",
		IsOutdated:       false,
		CheckedAt:        time.Now(),
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(cachePath, data, 0o644)

	// Override cache path
	brewStaleCachePath = func() string { return cachePath }

	getBrewInstalledVersion = func() string { return "1.2.3" }
	// checkBrewOutdated should NOT be called — cache hit
	called := false
	checkBrewOutdated = func() (bool, error) {
		called = true
		return true, nil // Would report outdated if called
	}

	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if called {
		t.Error("checkBrewOutdated was called despite valid cache")
	}
	if info.IsStale {
		t.Error("expected IsStale=false from cached result")
	}
}

func TestBrewCacheMissOnVersionChange(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = "Homebrew"

	origGetVersion := getBrewInstalledVersion
	origCheckOutdated := checkBrewOutdated
	origCachePath := brewStaleCachePath
	defer func() {
		getBrewInstalledVersion = origGetVersion
		checkBrewOutdated = origCheckOutdated
		brewStaleCachePath = origCachePath
	}()

	// Cache has old version
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "brew-stale-cache.json")
	entry := brewStaleCacheEntry{
		InstalledVersion: "1.2.2", // Old version in cache
		IsOutdated:       true,
		CheckedAt:        time.Now(),
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(cachePath, data, 0o644)

	brewStaleCachePath = func() string { return cachePath }

	// Current version is newer (user ran brew upgrade)
	getBrewInstalledVersion = func() string { return "1.2.3" }
	checkBrewOutdated = func() (bool, error) { return false, nil }

	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if info.IsStale {
		t.Error("expected IsStale=false after upgrade (cache miss on version change)")
	}
}

func TestBrewCacheMissOnTTLExpiry(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	Commit = "Homebrew"

	origGetVersion := getBrewInstalledVersion
	origCheckOutdated := checkBrewOutdated
	origCachePath := brewStaleCachePath
	defer func() {
		getBrewInstalledVersion = origGetVersion
		checkBrewOutdated = origCheckOutdated
		brewStaleCachePath = origCachePath
	}()

	// Cache with expired TTL
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "brew-stale-cache.json")
	entry := brewStaleCacheEntry{
		InstalledVersion: "1.2.3",
		IsOutdated:       false,
		CheckedAt:        time.Now().Add(-25 * time.Hour), // Expired
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(cachePath, data, 0o644)

	brewStaleCachePath = func() string { return cachePath }

	getBrewInstalledVersion = func() string { return "1.2.3" }
	called := false
	checkBrewOutdated = func() (bool, error) {
		called = true
		return true, nil // Now outdated
	}

	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if !called {
		t.Error("checkBrewOutdated was NOT called despite expired cache")
	}
	if !info.IsStale {
		t.Error("expected IsStale=true after TTL expiry recheck")
	}
}

func TestCheckStaleBinary_SourcePath(t *testing.T) {
	original := Commit
	defer func() { Commit = original }()

	// Set a hex commit to ensure source path is taken
	Commit = "abcdef1234567890"
	info := CheckStaleBinary(t.TempDir())
	if info == nil {
		t.Fatal("returned nil")
	}
	if info.InstallMethod != InstallMethodSource {
		// When the source path runs but git fails (temp dir), InstallMethod
		// should still be set to source if we got past the hex check
		if info.Error == nil {
			t.Errorf("InstallMethod = %q, want %q", info.InstallMethod, InstallMethodSource)
		}
	}
}
