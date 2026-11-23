package platform

import (
	"os"
	"strings"
	"testing"
)

// #298: the GOLDIE_CONTENT_DIR text says goldie validate reads it too, and the
// goldie server -dev section of the configuration reference says where log
// records and the export line go. The reference is generated from
// ConfigReference; the committed file must carry the same words (./run regen).

func committedReference(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// devSection returns the "## goldie server -dev" section of a reference.
func devSection(t *testing.T, ref string) string {
	t.Helper()
	const head = "## goldie server -dev\n"
	i := strings.Index(ref, head)
	if i < 0 {
		t.Fatalf("reference has no %q section", head)
	}
	rest := ref[i+len(head):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestContentDirSettingSaysValidateReadsItToo(t *testing.T) {
	var meaning string
	for _, s := range Settings {
		if s.Name == EnvContentDir {
			meaning = s.Meaning
		}
	}
	if meaning == "" {
		t.Fatalf("%s is not in the Settings registry", EnvContentDir)
	}
	if !strings.Contains(meaning, "goldie validate") {
		t.Errorf("%s description %q does not say goldie validate reads it", EnvContentDir, meaning)
	}
	// It still says goldie server requires it.
	if !strings.Contains(meaning, "goldie server") {
		t.Errorf("%s description %q no longer mentions goldie server", EnvContentDir, meaning)
	}
	for _, line := range strings.Split(committedReference(t), "\n") {
		if strings.HasPrefix(line, "| `"+EnvContentDir+"`") {
			if !strings.Contains(line, "goldie validate") {
				t.Errorf("committed reference row %q does not say goldie validate reads it (run: ./run regen)", line)
			}
			return
		}
	}
	t.Errorf("committed reference has no row for %s", EnvContentDir)
}

func TestServerDevSectionSaysWhereLogsAndTheExportLineGo(t *testing.T) {
	for name, ref := range map[string]string{
		"generated": string(ConfigReference(Settings)),
		"committed": committedReference(t),
	} {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			// Log records go to stderr; only the export line goes to stdout.
			if !strings.Contains(sec, "log records go to stderr") {
				t.Errorf("section does not say \"log records go to stderr\":\n%s", sec)
			}
			if !strings.Contains(sec, "only the export line") || !strings.Contains(sec, "stdout") {
				t.Errorf("section does not say only the export line goes to stdout:\n%s", sec)
			}
		})
	}
}

// eval "$(goldie server -dev)" blocks while the server runs, so the reference
// must never show it, and any usage it shows runs the server in the background
// or redirects its stdout to a file.
func TestServerDevSectionShowsNoBlockingUsage(t *testing.T) {
	for name, ref := range map[string]string{
		"generated": string(ConfigReference(Settings)),
		"committed": committedReference(t),
	} {
		t.Run(name, func(t *testing.T) {
			sec := devSection(t, ref)
			if strings.Contains(sec, "eval") {
				t.Errorf("section shows eval, which blocks while the server runs:\n%s", sec)
			}
			if strings.Contains(sec, "$(goldie server -dev") {
				t.Errorf("section substitutes the server's output in a command:\n%s", sec)
			}
			for _, line := range strings.Split(sec, "\n") {
				l := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "`"))
				if !strings.HasPrefix(l, "goldie server -dev") || strings.Contains(l, "serves") {
					continue
				}
				if !strings.Contains(l, "&") && !strings.Contains(l, ">") {
					t.Errorf("usage line %q neither backgrounds the server nor redirects stdout to a file", line)
				}
			}
		})
	}
}
