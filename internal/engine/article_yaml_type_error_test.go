package engine_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// The article parser shares splitFrontmatter and yamlErr with the agent parser
// (#405). A YAML type error in an article's frontmatter must keep rendering as
// one clean line: the yaml library puts a raw newline in it otherwise.
// (Articles read no description, so a non-scalar one is not an error there; the
// agent tests cover it.)
func TestArticleFrontmatterTypeErrorIsOneCleanLine(t *testing.T) {
	cases := map[string]string{
		"name is a list":     "---\nname: [x]\n---\nbody\n",
		"name is a map":      "---\nname: {a: b}\n---\nbody\n",
		"keywords is a map":  "---\nname: A\nkeywords: {a: b}\n---\nbody\n",
		"keywords block map": "---\nname: A\nkeywords:\n  a: b\n---\nbody\n",
		"two type errors":    "---\nname: [x]\nkeywords: {a: b}\n---\nbody\n",
	}
	lineOrKey := regexp.MustCompile(`line \d+|name|keywords`)
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			for _, file := range []string{"global/articles/a.md", "projects/" + govProject + "/articles/a.md", ".goldie/articles/a.md"} {
				msg := loadLocalErr(t, map[string]string{file: content}, file)
				if !strings.HasPrefix(msg, file+": malformed frontmatter") {
					t.Errorf("error = %q, want prefix %q", msg, file+": malformed frontmatter")
				}
				if strings.ContainsAny(msg, "\r\n") {
					t.Errorf("error is not a single line: %q", msg)
				}
				for _, r := range msg {
					if unicode.IsControl(r) {
						t.Errorf("error carries the control character %U: %q", r, msg)
						break
					}
				}
				if strings.Contains(msg, "/tmp") || strings.Contains(msg, os.TempDir()) {
					t.Errorf("error carries a host path: %q", msg)
				}
				if !lineOrKey.MatchString(strings.TrimPrefix(msg, file)) {
					t.Errorf("error names neither a line nor the key: %q", msg)
				}
			}
		})
	}
}
