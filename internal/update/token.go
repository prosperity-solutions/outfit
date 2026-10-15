package update

import "strings"

// Defaults of the two bases, the same as install.sh's. OUTFIT_BASE_URL and
// OUTFIT_API_URL replace them (for tests).
const (
	Repo           = "prosperity-solutions/outfit"
	DefaultBaseURL = "https://github.com/" + Repo
	DefaultAPIURL  = "https://api.github.com/repos/" + Repo
)

// tokenEnv is the one variable a token is read from. The repository and its releases are public,
// so outfit asks for no login. A token is used only when this is set on purpose (a private fork or
// mirror, or a network that rate-limits anonymous downloads). GH_TOKEN, GITHUB_TOKEN and `gh auth`
// are not looked at: an ambient token that is stale or for another account would only make a public
// download fail, and is one more place a credential gets sent. install.sh does the same.
const tokenEnv = "OUTFIT_GITHUB_TOKEN"

// Token is a GitHub token and where it came from (never its value, in any message).
type Token struct {
	Value  string
	Source string // "OUTFIT_GITHUB_TOKEN"
}

// FindToken reads OUTFIT_GITHUB_TOKEN. A value with characters a GitHub token never has
// is ignored, and the reason returned so the caller can say so.
func FindToken(getenv func(string) string) (tok Token, ignored string) {
	if v := strings.TrimSpace(getenv(tokenEnv)); v != "" {
		if !validToken(v) {
			return Token{}, "the GitHub token in " + tokenEnv + " has unexpected characters, so it was ignored"
		}
		return Token{Value: v, Source: tokenEnv}, ""
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
