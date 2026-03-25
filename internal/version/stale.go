// Package version provides version information and staleness checking for gt.
package version

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// These variables are set at build time via ldflags in cmd package.
// We provide fallback methods to read from build info.
var (
	// Commit can be set from cmd package or read from build info
	Commit = ""
)

// InstallMethod indicates how the gt binary was installed.
const (
	InstallMethodSource   = "source"
	InstallMethodHomebrew = "homebrew"
)

// StaleBinaryInfo contains information about binary staleness.
type StaleBinaryInfo struct {
	IsStale       bool   // True if binary commit doesn't match repo HEAD
	IsForward     bool   // True if repo HEAD is a descendant of binary commit (safe to rebuild)
	OnMainBranch  bool   // True if the repo is on the main branch
	BinaryCommit  string // Commit hash the binary was built from
	RepoCommit    string // Current repo HEAD commit
	CommitsBehind int    // Number of commits binary is behind (0 if unknown)
	InstallMethod string // How the binary was installed ("source" or "homebrew")
	Error         error  // Any error encountered during check
}

// resolveCommitHash gets the commit hash from build info or the Commit variable.
func resolveCommitHash() string {
	if Commit != "" {
		return Commit
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}

	return ""
}

// ShortCommit returns first 12 characters of a hash.
func ShortCommit(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// commitsMatch compares two commit hashes, handling different lengths.
// Returns true if one is a prefix of the other (minimum 7 chars to avoid false positives).
func commitsMatch(a, b string) bool {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	// Need at least 7 chars for a reasonable comparison
	if minLen < 7 {
		return false
	}
	return strings.HasPrefix(a, b[:minLen]) || strings.HasPrefix(b, a[:minLen])
}

// isHexCommit returns true if s looks like a git commit hash (all hex chars, 7+ chars).
func isHexCommit(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// brewStaleCacheEntry is the schema for ~/.cache/gt/brew-stale-cache.json.
type brewStaleCacheEntry struct {
	InstalledVersion string    `json:"installed_version"`
	IsOutdated       bool      `json:"is_outdated"`
	LatestVersion    string    `json:"latest_version,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

// brewStaleCacheTTL is how long a brew cache entry is valid before rechecking.
const brewStaleCacheTTL = 24 * time.Hour

// brewStaleCachePath returns the path to the brew staleness cache file.
// It's a variable so tests can override it.
var brewStaleCachePath = func() string {
	if cacheDir := os.Getenv("XDG_CACHE_HOME"); cacheDir != "" {
		return filepath.Join(cacheDir, "gt", "brew-stale-cache.json")
	}
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "gt", "brew-stale-cache.json")
}

// loadBrewCache loads the brew staleness cache. Returns nil if not found or invalid.
func loadBrewCache(path string) *brewStaleCacheEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry brewStaleCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	return &entry
}

// saveBrewCache writes a brew staleness cache entry.
func saveBrewCache(path string, entry *brewStaleCacheEntry) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// getBrewInstalledVersion returns the installed version of gt via brew.
// Returns "" if brew is not available or gt is not installed via brew.
var getBrewInstalledVersion = func() string {
	cmd := exec.Command("brew", "list", "--versions", "gt")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Output format: "gt 1.2.3" — extract the version part
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// checkBrewOutdated checks if gt is outdated via brew.
// Returns (isOutdated, error).
var checkBrewOutdated = func() (bool, error) {
	cmd := exec.Command("brew", "outdated", "--quiet", "gt")
	out, err := cmd.Output()
	if err != nil {
		// Exit code 1 with no output means not outdated (nothing to report)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	// If "gt" appears in the output, it's outdated
	return strings.Contains(strings.TrimSpace(string(out)), "gt"), nil
}

// checkHomebrewStaleness checks if a Homebrew-installed gt is outdated.
// Uses a file-based cache to avoid running brew on every command.
// On any error, returns IsStale=false (no false positives).
func checkHomebrewStaleness() *StaleBinaryInfo {
	info := &StaleBinaryInfo{
		InstallMethod: InstallMethodHomebrew,
		BinaryCommit:  "Homebrew",
	}

	cachePath := brewStaleCachePath()

	// Get currently installed version (local, no network)
	installed := getBrewInstalledVersion()
	if installed == "" {
		// Can't determine installed version — don't warn
		return info
	}

	// Check cache
	if cachePath != "" {
		if cached := loadBrewCache(cachePath); cached != nil {
			if cached.InstalledVersion == installed && time.Since(cached.CheckedAt) < brewStaleCacheTTL {
				info.IsStale = cached.IsOutdated
				return info
			}
		}
	}

	// Cache miss — check with brew
	outdated, err := checkBrewOutdated()
	if err != nil {
		// Brew check failed — don't warn
		return info
	}

	info.IsStale = outdated

	// Write cache
	saveBrewCache(cachePath, &brewStaleCacheEntry{
		InstalledVersion: installed,
		IsOutdated:       outdated,
		CheckedAt:        time.Now(),
	})

	return info
}

// CheckStaleBinary compares the binary's embedded commit with the repo HEAD.
// It returns staleness info including whether the binary needs rebuilding.
// This check is designed to be fast and non-blocking - errors are captured
// but don't interrupt normal operation.
func CheckStaleBinary(repoDir string) *StaleBinaryInfo {
	info := &StaleBinaryInfo{}

	// Get binary commit
	info.BinaryCommit = resolveCommitHash()
	if info.BinaryCommit == "" {
		info.Error = fmt.Errorf("cannot determine binary commit (dev build?)")
		return info
	}

	// If the binary commit is not a hex hash (e.g. "Homebrew"), this is a
	// package-manager install. Use the Homebrew staleness path instead of
	// comparing against the source repo HEAD. (gt-wiv)
	if !isHexCommit(info.BinaryCommit) {
		return checkHomebrewStaleness()
	}

	info.InstallMethod = InstallMethodSource

	// Get repo HEAD
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		info.Error = fmt.Errorf("cannot get repo HEAD: %w", err)
		return info
	}
	info.RepoCommit = strings.TrimSpace(string(output))

	// Check which branch the repo is on
	branchCmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	branchCmd.Dir = repoDir
	if branchOutput, err := branchCmd.Output(); err == nil {
		branch := strings.TrimSpace(string(branchOutput))
		info.OnMainBranch = (branch == "main" || branch == "master")
	}

	// Compare commits using prefix matching (handles short vs full hash)
	// Use the shorter of the two commit lengths for comparison
	if !commitsMatch(info.BinaryCommit, info.RepoCommit) {
		// Check if all commits between binary and HEAD only touch .beads/ files
		// (e.g., bd backup commits). These don't affect the binary and should not
		// trigger a stale warning. (GH#2596)
		if onlyBeadsChanges(repoDir, info.BinaryCommit) {
			// HEAD advanced but only via beads-only commits — not stale
			return info
		}

		info.IsStale = true

		// Check if this is a forward-only update (binary commit is ancestor of HEAD).
		// This prevents rebuilding to an older or diverged commit, which caused
		// a crash loop when a crew worktree's HEAD was behind the binary's commit.
		ancestorCmd := exec.Command("git", "merge-base", "--is-ancestor", info.BinaryCommit, "HEAD")
		ancestorCmd.Dir = repoDir
		info.IsForward = ancestorCmd.Run() == nil

		// Try to count commits between binary and HEAD
		countCmd := exec.Command("git", "rev-list", "--count", info.BinaryCommit+"..HEAD")
		countCmd.Dir = repoDir
		if countOutput, err := countCmd.Output(); err == nil {
			if count, parseErr := fmt.Sscanf(strings.TrimSpace(string(countOutput)), "%d", &info.CommitsBehind); parseErr != nil || count != 1 {
				info.CommitsBehind = 0
			}
		}
	}

	return info
}

// GetRepoRoot returns the git repository root for the gt source code.
// The canonical source is the gastown repo itself ($GT_ROOT/gastown).
// Crew rigs also contain cmd/gt/main.go but have different HEADs,
// so we prefer the gastown repo over CWD-based git toplevel detection.
func GetRepoRoot() (string, error) {
	// Check if GT_ROOT environment variable is set (agents always have this)
	if gtRoot := os.Getenv("GT_ROOT"); gtRoot != "" {
		candidates := []string{
			gtRoot + "/gastown",
			gtRoot + "/gastown/mayor/rig",
		}
		for _, candidate := range candidates {
			if hasGtSource(candidate) {
				return candidate, nil
			}
		}
	}

	// Try common development paths relative to home
	home := os.Getenv("HOME")
	if home != "" {
		candidates := []string{
			home + "/gt/gastown",
			home + "/gt/gastown/mayor/rig",
			home + "/gastown",
			home + "/gastown/mayor/rig",
			home + "/src/gastown",
			home + "/src/gastown/mayor/rig",
		}
		for _, candidate := range candidates {
			if hasGtSource(candidate) {
				return candidate, nil
			}
		}
	}

	// Fall back to current directory's git repo (may be a crew rig)
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	if output, err := cmd.Output(); err == nil {
		root := strings.TrimSpace(string(output))
		if hasGtSource(root) {
			return root, nil
		}
	}

	return "", fmt.Errorf("cannot locate gt source repository")
}

// isGitRepo checks if a directory is a git repository.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// hasGtSource checks if a directory contains the gt source code.
// We look for cmd/gt/main.go as the definitive marker.
func hasGtSource(dir string) bool {
	_, err := os.Stat(dir + "/cmd/gt/main.go")
	return err == nil
}

// onlyBeadsChanges checks whether all commits between binaryCommit and HEAD
// exclusively modify files under .beads/. Returns true if the diff contains
// no changes outside .beads/, meaning the binary is functionally up-to-date.
// Used to suppress false-positive stale warnings from bd backup commits. (GH#2596)
func onlyBeadsChanges(repoDir, binaryCommit string) bool {
	// Get files changed between binary commit and HEAD, excluding .beads/
	// If this produces no output, all changes are within .beads/
	cmd := exec.Command("git", "diff", "--name-only", binaryCommit+"..HEAD", "--", ".", ":!.beads")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		// Can't determine — be conservative, assume stale
		return false
	}
	return strings.TrimSpace(string(output)) == ""
}

// SetCommit allows the cmd package to pass in the build-time commit.
func SetCommit(commit string) {
	Commit = commit
}
