package update

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Defaults of the two bases, the same as install.sh's. OUTFIT_BASE_URL and
// OUTFIT_API_URL replace them (for tests).
const (
	Repo           = "prosperity-solutions/outfit"
	DefaultBaseURL = "https://github.com/" + Repo
	DefaultAPIURL  = "https://api.github.com/repos/" + Repo
)

// tokenEnv is the order tokens are looked for in, the same as install.sh's.
var tokenEnv = []string{"OUTFIT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

// Token is a GitHub token and where it came from (never its value, in any message).
type Token struct {
	Value  string
	Source string // "GH_TOKEN", "gh auth token", ...
}

// FindToken looks for a token in OUTFIT_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN, then
// the output of `gh auth token`. A value with characters a GitHub token never has
// is ignored, and the reason returned so the caller can say so.
func FindToken(getenv func(string) string, gh func() string) (tok Token, ignored string) {
	for _, name := range tokenEnv {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			if !validToken(v) {
				return Token{}, "the GitHub token in " + name + " has unexpected characters, so it was ignored"
			}
			return Token{Value: v, Source: name}, ""
		}
	}
	if gh != nil {
		if v := strings.TrimSpace(gh()); v != "" && validToken(v) {
			return Token{Value: v, Source: "gh auth token"}, ""
		}
	}
	return Token{}, ""
}

func validToken(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// GHAuthToken runs `gh auth token` for github.com, within ctx and at most 2 seconds.
// Empty when gh is missing, not logged in, or slow.
func GHAuthToken(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
