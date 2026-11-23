package platform

import (
	"strings"
	"testing"
)

// #281: the configuration reference says the client roles (the client
// commands and goldie mcp over stdio) export traces only. The reference is
// generated from ConfigReference; the committed file must carry the same words
// (./run regen). It relies on committedReference (content_dir_text_test.go).

// section returns the "## <title>" section of a reference.
func section(t *testing.T, ref, title string) string {
	t.Helper()
	head := "## " + title + "\n"
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

func TestConfigReferenceSaysClientRolesExportTracesOnly(t *testing.T) {
	for name, ref := range map[string]string{
		"generated": string(ConfigReference(Settings)),
		"committed": committedReference(t),
	} {
		t.Run(name, func(t *testing.T) {
			// One paragraph of the OpenTelemetry or the client section names
			// both client roles and says they export traces only.
			var found bool
			for _, title := range []string{"OpenTelemetry", "Client commands"} {
				for _, para := range strings.Split(section(t, ref, title), "\n\n") {
					p := strings.Join(strings.Fields(para), " ")
					if strings.Contains(p, "traces only") && strings.Contains(p, "goldie mcp") &&
						strings.Contains(p, "client commands") {
						found = true
					}
				}
			}
			if !found {
				t.Error("no paragraph of the OpenTelemetry or Client commands section says the client commands and " +
					"goldie mcp export \"traces only\"")
			}
		})
	}
}
