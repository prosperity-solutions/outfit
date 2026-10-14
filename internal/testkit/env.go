// Package testkit is outfit's test harness: the hermetic environment every test
// runs git in, a fixture factory for repositories, and the testscript commands.
package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HookLogEnv names the file the fake hooks append their argv and stdin to.
const HookLogEnv = "OUTFIT_TEST_HOOKLOG"

// EnvMap is the hermetic environment of HANDOVER.md §7.2, rooted at home.
// PATH is inherited so the git under test is the one on PATH.
func EnvMap(home string) map[string]string {
	return map[string]string{
		"HOME":                   home,
		"GIT_CONFIG_GLOBAL":      filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_AUTHOR_NAME":        "Test Author",
		"GIT_AUTHOR_EMAIL":       "author@example.com",
		"GIT_COMMITTER_NAME":     "Test Committer",
		"GIT_COMMITTER_EMAIL":    "committer@example.com",
		"GIT_AUTHOR_DATE":        "@1700000000 +0000",
		"GIT_COMMITTER_DATE":     "@1700000000 +0000",
		"TZ":                     "UTC",
		"LC_ALL":                 "C",
		"GIT_TERMINAL_PROMPT":    "0",
		"OUTFIT_NO_UPDATE_CHECK": "1",
		"PATH":                   os.Getenv("PATH"),
	}
}

// HermeticEnviron is the process environment with ambient git variables removed
// (so a test run from inside a hook cannot leak into another repo), overlaid with
// Env(home).
func HermeticEnviron(home string) []string {
	return append(scrub(os.Environ()), Env(home)...)
}

// Env is EnvMap as KEY=VALUE pairs.
func Env(home string) []string {
	m := EnvMap(home)
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// GlobalConfig is the content of the hermetic ~/.gitconfig (init.defaultBranch=main).
const GlobalConfig = "[init]\n\tdefaultBranch = main\n[advice]\n\tdetachedHead = false\n"

// WriteHome creates home with the hermetic global git config.
func WriteHome(home string) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(GlobalConfig), 0o644)
}

// leaks reports whether an environment variable would make git operate on some
// other repository, read other configuration, or write traces into a test.
func leaks(k string) bool {
	switch k {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
		"GIT_PREFIX", "GIT_NAMESPACE", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CEILING_DIRECTORIES",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG", "GIT_CONFIG_SYSTEM":
		return true
	}
	return strings.HasPrefix(k, "GIT_TRACE") || strings.HasPrefix(k, "GIT_CONFIG_KEY_") ||
		strings.HasPrefix(k, "GIT_CONFIG_VALUE_")
}

// scrub removes the variables in leaks from env.
func scrub(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !leaks(k) {
			out = append(out, kv)
		}
	}
	return out
}

// Isolate removes ambient git variables from this test process for the duration
// of t, so a package test that runs git directly (not through a fixture) is
// hermetic even when `go test` runs inside a hook or under `git -c`.
func Isolate(t testing.TB) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); leaks(k) {
			t.Setenv(k, "") // registers the restore
			_ = os.Unsetenv(k)
		}
	}
}
