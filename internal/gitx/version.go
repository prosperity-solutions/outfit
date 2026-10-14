package gitx

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// Version is a parsed git version. Only the first three numeric components
// matter; vendor suffixes such as "(Apple Git-154)" are ignored.
type Version struct {
	Major, Minor, Patch int
}

// Floor is the oldest git outfit supports. 2.36 added the sparse-checkout and
// worktree-config behaviour outfit relies on.
var Floor = Version{2, 36, 0}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion accepts the output of `git --version` or a bare "2.39.5".
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("cannot parse a git version from %q", s)
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

// FloorError is returned by CheckFloor when git is too old. Its text names the
// fix: agents follow error text.
type FloorError struct {
	Found Version
}

func (e *FloorError) Error() string {
	return fmt.Sprintf("git %s is older than the minimum outfit supports (%s). "+
		"Upgrade git: on Ubuntu 22.04 (which ships 2.34) run "+
		"`sudo add-apt-repository ppa:git-core/ppa && sudo apt update && sudo apt install git`; "+
		"on macOS run `brew install git`", e.Found, Floor)
}

// CheckFloor returns a *FloorError when v is below Floor.
func CheckFloor(v Version) error {
	if v.Less(Floor) {
		return &FloorError{Found: v}
	}
	return nil
}

// Version runs `git --version`. It is NOT for hot paths: sync, get and hook
// trust the version recorded in state.json at init.
func (c Client) Version(ctx context.Context) (Version, string, error) {
	res, err := c.Read(ctx, "--version")
	if err != nil {
		return Version{}, "", err
	}
	raw := string(bytes.TrimSpace(res.Stdout))
	v, err := ParseVersion(raw)
	return v, raw, err
}

// RequireFloor runs `git --version` and enforces Floor. Used only by init, use,
// status and version.
func (c Client) RequireFloor(ctx context.Context) (Version, error) {
	v, _, err := c.Version(ctx)
	if err != nil {
		return Version{}, err
	}
	return v, CheckFloor(v)
}
