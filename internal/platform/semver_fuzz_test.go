package platform

import (
	"strings"
	"testing"
)

// ParseSemver never panics, its only error is a fixed message that cannot
// contain the value (PC-05), and Less is a strict order on what it accepts:
// irreflexive and asymmetric. String parses back to the same precedence and
// never carries build metadata.
func FuzzParseSemver(f *testing.F) {
	for _, s := range []string{"", "1.4.0", "1.4.0-rc.1", "1.0.0-alpha.1+b.2", "01.2.3", "1.2.3-", "99999999999999999999.0.0", "1.2.3\x00"} {
		f.Add(s, "1.4.0")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		va, errA := ParseSemver(a)
		vb, errB := ParseSemver(b)
		for _, c := range []struct {
			in  string
			err error
		}{{a, errA}, {b, errB}} {
			if c.err != nil && c.err.Error() != errSemver.Error() {
				t.Errorf("%q: error %q is not the fixed message, so it may echo the value", c.in, c.err)
			}
		}
		if errA != nil || errB != nil {
			return
		}
		if strings.Contains(va.String(), "+") {
			t.Errorf("%q: String %q keeps build metadata", a, va.String())
		}
		if back, err := ParseSemver(va.String()); err != nil || back.Less(va) || va.Less(back) {
			t.Errorf("%q: String %q does not parse back to the same precedence (%v)", a, va.String(), err)
		}
		if va.Less(va) {
			t.Errorf("%q < itself", a)
		}
		if va.Less(vb) && vb.Less(va) {
			t.Errorf("%q and %q are each less than the other", a, b)
		}
	})
}
