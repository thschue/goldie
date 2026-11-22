package platform

import (
	"errors"
	"strconv"
	"strings"
)

// Semver is a parsed semantic version (semver 2.0.0). Build metadata is
// dropped: it takes no part in precedence.
type Semver struct {
	core [3]uint64 // MAJOR, MINOR, PATCH
	pre  []string  // prerelease identifiers; none for a release
}

// errSemver does not echo the value: it may come from a _FILE (PC-05) or a
// request header.
var errSemver = errors.New("must be a semantic version MAJOR.MINOR.PATCH, such as 1.4.0, optionally followed by -<prerelease>")

// ParseSemver reads a semantic version exactly as semver 2.0.0 defines it:
// MAJOR.MINOR.PATCH in ASCII digits without leading zeros, optionally
// followed by -<prerelease> and +<build>, each one or more dot-separated
// identifiers of ASCII letters, digits and hyphens, with no leading zeros in
// a numeric prerelease identifier. No leading v, sign, space or range is
// accepted, nor a part too large for a uint64. Errors never contain the
// value.
func ParseSemver(s string) (Semver, error) {
	rest, build, hasBuild := strings.Cut(s, "+")
	if hasBuild && !identifiers(build, false) {
		return Semver{}, errSemver
	}
	core, pre, hasPre := strings.Cut(rest, "-")
	if hasPre && !identifiers(pre, true) {
		return Semver{}, errSemver
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Semver{}, errSemver
	}
	var v Semver
	for i, p := range parts {
		if !numeric(p) {
			return Semver{}, errSemver
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Semver{}, errSemver
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

// String writes v as MAJOR.MINOR.PATCH, followed by -<prerelease> when it
// has one. Build metadata was dropped by ParseSemver, so it never appears.
func (v Semver) String() string {
	s := strconv.FormatUint(v.core[0], 10) + "." + strconv.FormatUint(v.core[1], 10) + "." + strconv.FormatUint(v.core[2], 10)
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

// IsZero reports whether v is the zero Semver, the one an unset
// GOLDIE_MIN_CLIENT_VERSION leaves in Config: no gate. 0.0.0 parses to it too,
// and as a minimum it would refuse only 0.0.0 prereleases.
func (v Semver) IsZero() bool { return v.core == [3]uint64{} && len(v.pre) == 0 }

// Less reports whether v has lower precedence than o (semver 2.0.0, section
// 11): the core numerically, then a prerelease below its release, then the
// prerelease identifiers in order.
func (v Semver) Less(o Semver) bool {
	for i := range v.core {
		if v.core[i] != o.core[i] {
			return v.core[i] < o.core[i]
		}
	}
	switch {
	case len(v.pre) == 0:
		return false
	case len(o.pre) == 0:
		return true
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		if c := compareIdentifier(v.pre[i], o.pre[i]); c != 0 {
			return c < 0
		}
	}
	return len(v.pre) < len(o.pre)
}

// compareIdentifier orders two prerelease identifiers: numeric ones as
// numbers, below alphanumeric ones, which compare in ASCII order. Numeric
// identifiers have no leading zeros, so the longer is the larger, and no
// number is too large to compare.
func compareIdentifier(a, b string) int {
	an, bn := numeric(a), numeric(b)
	switch {
	case an && bn:
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// identifiers reports whether s is one or more dot-separated non-empty
// identifiers of ASCII letters, digits and hyphens; with noLeadingZero, a
// numeric identifier other than 0 must not start with 0.
func identifiers(s string, noLeadingZero bool) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for i := 0; i < len(id); i++ {
			c := id[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' {
				return false
			}
		}
		if noLeadingZero && len(id) > 1 && id[0] == '0' && allDigits(id) {
			return false
		}
	}
	return true
}

// numeric reports whether s is a decimal number in ASCII digits without a
// leading zero, as semver requires of the core and numeric identifiers.
func numeric(s string) bool {
	return allDigits(s) && (len(s) == 1 || s[0] != '0')
}

// allDigits reports whether s is one or more ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
