package platform

import (
	"testing"
	"time"
)

// ParseRateLimit never panics, accepts only positive counts per second or per
// minute, and its only error is a fixed message that cannot contain the value
// (PC-05).
func FuzzParseRateLimit(f *testing.F) {
	for _, s := range []string{"", "20/s", "3/m,burst=6", "0/s", "20/s,burst=", "99999999999999999999/s", "20/s\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l, err := ParseRateLimit(s)
		if err != nil {
			if err.Error() != errRateLimit.Error() {
				t.Errorf("%q: error %q is not the fixed message, so it may echo the value", s, err)
			}
			return
		}
		if l.Requests <= 0 || l.Burst <= 0 || (l.Per != time.Second && l.Per != time.Minute) {
			t.Errorf("%q parsed to %+v", s, l)
		}
	})
}
