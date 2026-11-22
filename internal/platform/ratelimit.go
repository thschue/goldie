package platform

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// RateLimit is a parsed GOLDIE_RATE_LIMIT: Requests per Per for each token,
// in bursts of up to Burst requests.
type RateLimit struct {
	Requests int
	Per      time.Duration // time.Second or time.Minute
	Burst    int
}

// errRateLimit does not echo the value: it may have been read from a _FILE
// (PC-05).
var errRateLimit = errors.New("must be <n>/s or <n>/m, optionally followed by ,burst=<b>, with n and b positive integers")

// ParseRateLimit reads "<n>/s" or "<n>/m", optionally followed by
// ",burst=<b>"; b defaults to n, and "" is the registered default. n and b
// are positive ASCII decimal integers without sign or spaces, and the unit is
// lower case. Nothing else is accepted, and errors never contain the value.
func ParseRateLimit(s string) (RateLimit, error) {
	if s == "" {
		s = def(EnvRateLimit)
	}
	rate, opt, hasOpt := strings.Cut(s, ",")
	num, unit, _ := strings.Cut(rate, "/")
	n, ok := positive(num)
	if !ok {
		return RateLimit{}, errRateLimit
	}
	l := RateLimit{Requests: n, Burst: n}
	switch unit {
	case "s":
		l.Per = time.Second
	case "m":
		l.Per = time.Minute
	default:
		return RateLimit{}, errRateLimit
	}
	if hasOpt {
		b, found := strings.CutPrefix(opt, "burst=")
		if l.Burst, ok = positive(b); !found || !ok {
			return RateLimit{}, errRateLimit
		}
	}
	return l, nil
}

// positive parses a positive ASCII decimal integer: digits only, no sign.
func positive(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}
