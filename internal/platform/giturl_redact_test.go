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
		"https://u:p?x#y@h/c.git":               "https://(redacted)",
		// An @ after a ?, # or byte a URL cannot hold may sit in a query or
		// fragment, so nothing after the scheme is kept.
		"https://h/r.git?password=p@ssw0rd":     "https://(redacted)",
		"https://h/r.git#tok=p@ssw0rd":          "https://(redacted)",
		"https://h/r.git?a=p@ss#f=q@rd":         "https://(redacted)",
		"https://u:p@h/r.git?a=p@ssw0rd":        "https://(redacted)",
		"git@h:org/c.git?password=p@ssw0rd":     "(redacted)",
		"h/r.git#p@ssw0rd":                      "(redacted)",
		"https://h/r.git?password=p%40ssw0rd":   "https://h/r.git?(redacted)",
		"https://u:p%40ss@h/r.git":              "https://(redacted)@h/r.git",
		"https://h/r.git\npw=p@ssw0rd":          "https://(redacted)",
		"https://h/r.git\u202e@ssw0rd":          "https://(redacted)",
		"https://h/org/repo@v2.git":             "https://(redacted)@v2.git",
		"https://u:p@h/r.git\n\"}{\"x\":\"p\"}": "https://(redacted)@h/r.git(redacted)",
		"ssh://git@h:2222/org/c.git":            "ssh://(redacted)@h:2222/org/c.git",
		"://u:p@h/c.git":                        "(redacted)@h/c.git",
		"1https://u@h/c.git":                    "(redacted)@h/c.git",
		"https://bücher.example/c.git":          "https://b(redacted)",
		"file:///srv/content.git":               "file:///srv/content.git",
		// #392: the shapes engine's old redactor leaked; there is one
		// redactor now, and it keeps neither password.
		"deploy:/pw@host:org/r.git": "(redacted)@host:org/r.git",
		"https://u:123/w@host/x":    "https://(redacted)@host/x",
	} {
		if got := redactGitURL(raw); got != want {
			t.Errorf("redactGitURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
