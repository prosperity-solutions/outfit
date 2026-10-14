package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version, MAJOR.MINOR.PATCH.
type Version [3]int

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// ParseVersion reads "0.3.1" or "v0.3.1". A pre-release or build suffix
// ("0.3.1-SNAPSHOT-abc") is not a release and is refused, as is "dev".
func ParseVersion(s string) (Version, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(t, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%q is not a version like 0.3.1", s)
	}
	var v Version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return Version{}, fmt.Errorf("%q is not a version like 0.3.1", s)
		}
		v[i] = n
	}
	return v, nil
}
