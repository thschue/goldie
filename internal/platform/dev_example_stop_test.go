package platform

import (
	"regexp"
	"strings"
	"testing"
)

// #331: the goldie server -dev example backgrounds the server inside a
// subshell, so the shell has no job for `kill %1` or `fg`. The section must say
// what stops it, and the privacy step must sit in the example's own span.

// A way to stop a server that is not a job of this shell: pkill or killall by
// name, or kill with a process id (a number, $!, $(pgrep ...) or <pid>) rather
// than a job spec.
var stopCommand = regexp.MustCompile(`\b(pkill|killall)\b|\bkill\s+(-\w+\s+)*(\$\(pgrep|\$!|\$\w*pid|<pid>|\d+)`)

// A job-control way to stop it, which a subshell-started server does not offer.
var jobControl = regexp.MustCompile(`\bkill\s+(-\w+\s+)*%|(^|[\s;&(])fg\b`)

func TestServerDevSectionSaysHowToStopTheServer(t *testing.T) {
	for name, ref := range devReferences(t) {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			found := false
			for _, span := range backtickedSpans(sec) {
				if jobControl.MatchString(span) {
					t.Errorf("span %q stops the server with job control (kill %%N or fg), which a server started in a subshell does not have", span)
				}
				if stopCommand.MatchString(span) {
					found = true
				}
			}
			if !found {
				t.Errorf("section names no way to stop a server started in a subshell (for example `pkill -f 'goldie server -dev'`; kill %%1 and fg do not work there):\n%s", sec)
			}
		})
	}
}

// The privacy step must be inside the same backticked span as the redirect to
// dev.env, and before it, so prose cannot satisfy it and the token file is
// never briefly world-readable.
func TestServerDevExampleSpanCreatesTheTokenFilePrivately(t *testing.T) {
	for name, ref := range devReferences(t) {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			examples := 0
			for _, span := range backtickedSpans(sec) {
				redirect := strings.Index(span, "> dev.env")
				if redirect < 0 {
					continue
				}
				examples++
				private := -1
				for _, w := range []string{"umask 077", "chmod 600"} {
					if i := strings.Index(span, w); i >= 0 && (private < 0 || i < private) {
						private = i
					}
				}
				if private < 0 {
					t.Errorf("span %q redirects to dev.env without umask 077 or chmod 600 in the same span; it holds the API token", span)
				} else if private > redirect {
					t.Errorf("span %q sets the mode after the redirect to dev.env, so the token file is briefly world-readable", span)
				}
			}
			if examples == 0 {
				t.Errorf("no backticked span redirects to dev.env:\n%s", sec)
			}
		})
	}
}
