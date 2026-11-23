package platform

import "testing"

// redactGitURL's exact output: what survives is the scheme, the host and the
// path; userinfo, query, fragment and anything after a byte a URL cannot hold
// become (redacted). An @ in a path costs the host, never a password.
func TestRedactGitURLKeepsOnlySchemeHostAndPath(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                      "",
		"https://git.example.com/org/c.git":     "https://git.example.com/org/c.git",
		"https://u:p@git.example.com/org/c.git": "https://(redacted)@git.example.com/org/c.git",
		"HTTPS://u:p@h/c.git?private_token=x#f": "HTTPS://(redacted)@h/c.git?(redacted)",
		"https://h/c.git#frag":                  "https://h/c.git#(redacted)",
		"u:p@git.example.com:org/c.git":         "(redacted)@git.example.com:org/c.git",
		"https://u:p?x#y@h/c.git":               "https://(redacted)@h/c.git",
		"https://h/org/repo@v2.git":             "https://(redacted)@v2.git",
		"https://u:p@h/r.git\n\"}{\"x\":\"p\"}": "https://(redacted)@h/r.git(redacted)",
		"ssh://git@h:2222/org/c.git":            "ssh://(redacted)@h:2222/org/c.git",
		"://u:p@h/c.git":                        "(redacted)@h/c.git",
		"1https://u@h/c.git":                    "(redacted)@h/c.git",
		"https://bücher.example/c.git":          "https://b(redacted)",
		"file:///srv/content.git":               "file:///srv/content.git",
	} {
		if got := redactGitURL(raw); got != want {
			t.Errorf("redactGitURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
