package engine

import (
	"regexp"
	"strings"
)

// redaction is one pattern and what replaces each match: "[redacted]" for the
// whole match, or the groups it keeps.
type redaction struct {
	re   *regexp.Regexp
	repl string
}

const redacted = "[redacted]"

// credValue is a credential's value: a closed quote, an unterminated quote
// (which takes the rest of the message, as nothing says where the value ends)
// or one word, after an optional auth scheme.
const credValue = `((bearer|basic|token)\s+)?("[^"]*"|'[^']*'|["'][\s\S]*|\S+)`

// The patterns run in order. They remove what a Source error must never show:
// URLs, credentials, absolute paths and network addresses. Each Source already
// returns safe errors (a file named relative to its root, a fixed text for a
// transport failure), so this is the last line of defence, and it leaves a
// root-relative path, a version string and a plain word such as "keyword"
// alone. Where in doubt about a credential it removes too much: a removed word
// costs a little detail, a leaked one may cost a credential.
var redactions = []redaction{
	// URLs, userinfo included.
	{regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S+`), redacted},
	// A credential-like name (also inside an identifier such as api_key or
	// GOLDIE_TOKEN), an optional auth scheme, and the value.
	{regexp.MustCompile(`(?i)[\w.-]*(token|passw(or)?d|passphrase|pwd|secret|authorization|bearer|credential|cookie|session)[\w.-]*["']?\s*[:=]?\s*` + credValue), redacted},
	// Short names only with a : or =, so "keyword", "passed" and "author" stay:
	// pw, pass, tok and sid as a whole word of an identifier, key and auth also
	// at the end of a compound such as privatekey or basicauth. A closing quote
	// may sit before the separator, as in JSON.
	{regexp.MustCompile(`(?i)(\b([\w.-]*[_.-])?(pw|pass|tok|sid)|[\w.-]*(key|auth))([_.-][\w.-]*)?["']?\s*[:=]\s*` + credValue), redacted},
	// Userinfo without a scheme (user:password@host, git@host:path) and email
	// addresses. It runs after the credential patterns so it cannot swallow
	// the closing quote of a quoted value.
	{regexp.MustCompile(`\S+@\S+`), redacted},
	// Bare tokens with a known prefix, however short.
	{regexp.MustCompile(`\b(gh[pousr]_|github_pat_|glpat-|xox[a-z]-|sk-|AKIA)[\w-]+`), redacted},
	// Absolute paths: POSIX, home-relative, Windows drive and UNC.
	{regexp.MustCompile(`(^|[\s"'(=,;:` + "`" + `])(~?/[^\s"'` + "`" + `]*|[A-Za-z]:\\[^\s"'` + "`" + `]*|\\\\[^\s"'` + "`" + `]*)`), "${1}" + redacted},
	// Addresses: bracketed or bare IPv6, then IPv4, then host:port.
	{regexp.MustCompile(`\[[^\]\s]*\](:\d+)?`), redacted},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{0,4}(:[0-9a-f]{0,4}){2,7}\b`), redacted},
	{regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`), redacted},
	{regexp.MustCompile(`[\w-]+:\d+\b`), redacted},
	// The host of a failed lookup or resolution, dotless or not.
	{regexp.MustCompile(`(?i)\b(resolve hostname\s+)[^\s:]+`), "${1}" + redacted},
	// The host of a failed lookup, dotless or not.
	{regexp.MustCompile(`(?i)\b(lookup\s+)[^\s:]+(\s+on\s|:\s+no such host)`), "${1}" + redacted + "${2}"},
}

var (
	// longRun is a long unbroken run: a token, a hash or base64 text.
	longRun = regexp.MustCompile(`[A-Za-z0-9_+=-]{32,}`)
	// dotted is a dotted name: a host, a file or a version string.
	dotted = regexp.MustCompile(`[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)+`)
)

// redact strips URLs, credential-looking values, absolute paths and network
// addresses from a message, so a Source error is safe to show on a status
// endpoint.
func redact(msg string) string {
	for _, r := range redactions {
		msg = r.re.ReplaceAllString(msg, r.repl)
	}
	msg = replaceIf(longRun, msg, func(_, run string) bool {
		// A kebab-case file name is long but has no digit or capital; a
		// token, hash or base64 string does.
		return strings.ContainsAny(run, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	})
	return replaceIf(dotted, msg, func(before, name string) bool {
		if strings.HasSuffix(before, "/") || strings.HasSuffix(before, `\`) {
			return false // a root-relative path such as global/articles/a.md
		}
		last := name[strings.LastIndex(name, ".")+1:]
		if len(last) < 2 || strings.Trim(last, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return false // a version string such as 1.4.0-rc.2, or "e.g"
		}
		switch strings.ToLower(last) {
		case "md", "yaml", "yml", "json":
			return false // a file at the Source root
		}
		return true // a host name such as git.internal.corp
	})
}

// replaceIf redacts each match of re in msg for which redactIt, given the text
// before the match and the match, says so.
func replaceIf(re *regexp.Regexp, msg string, redactIt func(before, match string) bool) string {
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(msg, -1) {
		if !redactIt(msg[:loc[0]], msg[loc[0]:loc[1]]) {
			continue
		}
		b.WriteString(msg[last:loc[0]])
		b.WriteString(redacted)
		last = loc[1]
	}
	b.WriteString(msg[last:])
	return b.String()
}
