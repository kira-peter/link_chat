// Package version is the single source of the release version shared by the
// server and the client (spec build-version).
//
// The version comes from the VERSION file at the repository root, embedded at
// compile time, so a plain `go build` reports the same number as a release
// build. It is deliberately not a linker variable: a forgotten -ldflags would
// silently ship "dev".
package version

import (
	"errors"
	"strconv"
	"strings"

	linkchat "link-chat"
)

// Version is a parsed x.y.z release version.
type Version struct {
	Major, Minor, Patch int
}

// String formats v as x.y.z.
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// ErrInvalid reports a string that is not an x.y.z release version.
var ErrInvalid = errors.New("version must be x.y.z with non-negative integers and no leading zeros")

// Parse reads an x.y.z version. Each part is a decimal integer without leading
// zeros (other than 0 itself); no "v" prefix and no suffix are allowed.
func Parse(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, ErrInvalid
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return Version{}, ErrInvalid
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return Version{}, ErrInvalid
			}
		}
		n[i], _ = strconv.Atoi(p)
	}
	return Version{n[0], n[1], n[2]}, nil
}

// Compare returns -1, 0 or 1 as a is older than, equal to or newer than b.
func Compare(a, b Version) int {
	for _, d := range [3]int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// Less reports whether a is older than b.
func (a Version) Less(b Version) bool { return Compare(a, b) < 0 }

var (
	current      Version
	currentValid bool
)

func init() {
	v, err := Parse(strings.TrimSpace(linkchat.RawVersion))
	current, currentValid = v, err == nil
}

// Current is the version of this build. It is the zero Version when the VERSION
// file is malformed; the package's tests fail in that case, so a release never
// ships it.
func Current() Version { return current }

// String is the version of this build as x.y.z.
func String() string { return current.String() }

// Valid reports whether the VERSION file held a well-formed version.
func Valid() bool { return currentValid }

// ClientTag formats the handshake identification "<name>/<x.y.z>" for this
// build.
func ClientTag(name string) string { return name + "/" + String() }

// ParseTag splits a handshake identification into its program name and
// version. ok is false when there is no "/" or the version part is not a valid
// x.y.z, which is what an older peer that only sends a name looks like.
func ParseTag(tag string) (name string, v Version, ok bool) {
	i := strings.LastIndexByte(tag, '/')
	if i < 0 {
		return tag, Version{}, false
	}
	v, err := Parse(tag[i+1:])
	if err != nil {
		return tag[:i], Version{}, false
	}
	return tag[:i], v, true
}
