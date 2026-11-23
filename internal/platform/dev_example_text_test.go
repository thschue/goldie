package platform

import (
	"strings"
	"testing"
)

// #324: the goldie server --dev example in the configuration reference creates
// dev.env privately (it holds the API token) and says when to source it. The
// reference is generated from ConfigReference; the committed file must carry
// the same words (./run regen).

func devReferences(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"generated": string(ConfigReference(Settings)),
		"committed": committedReference(t),
	}
}

// backtickedSpans returns the text between each pair of backticks, with runs of
// whitespace (including line breaks) collapsed to one space.
func backtickedSpans(sec string) []string {
	parts := strings.Split(sec, "`")
	var spans []string
	for i := 1; i < len(parts); i += 2 {
		spans = append(spans, strings.Join(strings.Fields(parts[i]), " "))
	}
	return spans
}

func TestServerDevSectionCreatesTheTokenFilePrivately(t *testing.T) {
	for name, ref := range devReferences(t) {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			redirect := strings.Index(sec, "> dev.env")
			if redirect < 0 {
				t.Fatalf("section has no example redirecting to dev.env:\n%s", sec)
			}
			private := -1
			for _, w := range []string{"umask 077", "chmod 600"} {
				if i := strings.Index(sec, w); i >= 0 && (private < 0 || i < private) {
					private = i
				}
			}
			if private < 0 {
				t.Fatalf("section does not create dev.env privately (umask 077 or chmod 600); it holds the API token:\n%s", sec)
			}
			// The file must be private before the token is written to it.
			if private > redirect {
				t.Errorf("umask 077 or chmod 600 comes after the redirect to dev.env, so the token file is briefly world-readable:\n%s", sec)
			}
		})
	}
}

func TestServerDevSectionSaysToSourceDevEnvOnceTheServerIsUp(t *testing.T) {
	for name, ref := range devReferences(t) {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			if !strings.Contains(sec, "`. ./dev.env`") {
				t.Errorf("section does not tell the reader to run `. ./dev.env`:\n%s", sec)
			}
			if !strings.Contains(sec, "once the server is up") {
				t.Errorf("section does not say dev.env is read once the server is up (it is empty until then):\n%s", sec)
			}
		})
	}
}

// Every backticked span that runs goldie server --dev, other than the synopsis
// `goldie server --dev [dir]`, backgrounds the server or redirects its stdout,
// wherever it sits in a sentence. The section must show at least one.
func TestServerDevSectionChecksEveryBacktickedUsage(t *testing.T) {
	for name, ref := range devReferences(t) {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			examples := 0
			for _, span := range backtickedSpans(sec) {
				if !strings.Contains(span, "goldie server --dev") || span == "goldie server --dev [dir]" {
					continue
				}
				// A stop command names the server, it does not run it. It must be one
				// command: no ;, &, | or substitution that could smuggle a launch in.
				if (strings.HasPrefix(span, "pkill ") || strings.HasPrefix(span, "killall ")) &&
					!strings.ContainsAny(span, ";&|$`\n") {
					continue
				}
				examples++
				if strings.Contains(span, "eval") || strings.Contains(span, "$(") {
					t.Errorf("usage %q substitutes or evals the server's output, which blocks while it runs", span)
				}
				if !strings.Contains(span, "&") && !strings.Contains(span, ">") {
					t.Errorf("usage %q neither backgrounds the server nor redirects stdout to a file", span)
				}
			}
			if examples == 0 {
				t.Errorf("section shows no goldie server --dev example:\n%s", sec)
			}
		})
	}
}
