package app

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// A PTR answer is DNS data, so hostile. peerName never panics, and a name it
// gives is the answer's first label, lower-cased: a DNS label as written, all
// ASCII, no address in disguise, and the rest of the answer is the configured
// name's labels, optionally followed by more (#398).
func FuzzPeerName(f *testing.F) {
	for _, s := range []string{
		"goldie-0.goldie-headless.goldie.svc.cluster.local.",
		"GOLDIE-1.goldie-headless.goldie.svc",
		"10-0-0-7.goldie-headless.goldie.svc.cluster.local.",
		"fd00--15.goldie-headless.goldie.svc.",
		"Koldie-5.goldie-headless.goldie.svc.",
		"goldie-headless.goldie.svc.",
		"goldie-6.xgoldie-headless.goldie.svc.",
		"goldie\x00.goldie-headless.goldie.svc",
		"",
		".",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ptr string) {
		name, ok := peerName([]string{ptr}, headless)
		if !ok {
			return
		}
		label, rest, _ := strings.Cut(strings.TrimSuffix(ptr, "."), ".")
		if !platform.IsDNSLabel(name) || name != strings.ToLower(name) || name != strings.ToLower(label) {
			t.Errorf("%q gave the name %q, not its first label lower-cased", ptr, name)
		}
		if dashedIP(name) {
			t.Errorf("%q gave the dashed IP %q", ptr, name)
		}
		if r := strings.ToLower(rest) + "."; !strings.HasPrefix(r, headless+".") {
			t.Errorf("%q was accepted, but %q is not under %q", ptr, rest, headless)
		}
	})
}
