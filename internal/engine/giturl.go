package engine

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// GitOption names the GitOptions field a NewGitSource error is about, so a
// caller can say which of its own settings to fix.
type GitOption string

// The GitOptions fields, as GitOptionError names them.
const (
	GitOptionURL        GitOption = "URL"
	GitOptionRef        GitOption = "Ref"
	GitOptionToken      GitOption = "Token"
	GitOptionSSHKey     GitOption = "SSHKey"
	GitOptionKnownHosts GitOption = "KnownHosts"
	GitOptionCABundle   GitOption = "CABundle"
)

// GitOptionError is every NewGitSource error: the option to fix and a fixed
// message that never holds the URL, the ref or a credential.
type GitOptionError struct {
	Option GitOption
	err    error
}

func (e *GitOptionError) Error() string { return e.err.Error() }

func (e *GitOptionError) Unwrap() error { return e.err }

// optionError is a GitOptionError with the fixed message "git source: " msg.
func optionError(opt GitOption, msg string) error {
	return &GitOptionError{Option: opt, err: errors.New("git source: " + msg)}
}

// errURLForm is the refusal of a URL of no accepted form.
const errURLForm = "URL must be HTTPS or SSH with a host, user@host:path, or FILE with an absolute path"

// schemeForm is go-git's test for a URL with a scheme: anything else it reads
// as scp-like or as a local path.
var schemeForm = regexp.MustCompile(`^[^:]+://`)

// scpForm is go-git's pattern for an scp-like URL, [user@]host:[port:]path,
// so what this package accepts as scp-like is what go-git connects to.
var scpForm = regexp.MustCompile(`^(?:(?P<user>[^@]+)@)?(?P<host>[^:\s]+):(?:(?P<port>[0-9]{1,5}):)?(?P<path>[^\\].*)$`)

// parseGitURL reads raw the way go-git will: a URL with a scheme through
// url.Parse, and an scp-like one, user@host:path, as the ssh URL it stands
// for. The result is only checked, never printed, and go-git is handed raw.
//
// An scp-like URL needs its user: without one, host:path cannot be told from
// a scheme and its opaque rest (file:path), and <transport>::address, git's
// remote helper syntax (ext::, fd::), is refused outright. Neither the user
// nor the host may start with "-", and the user holds no password.
func parseGitURL(raw string) (*url.URL, error) {
	if schemeForm.MatchString(raw) {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, optionError(GitOptionURL, "URL is not valid")
		}
		return u, nil
	}
	m := scpForm.FindStringSubmatch(raw)
	before, _, _ := strings.Cut(raw, ":")
	if m == nil || strings.Contains(before, "/") || strings.Contains(raw, "::") {
		return nil, optionError(GitOptionURL, errURLForm)
	}
	user, host, port, path := m[1], m[2], m[3], m[4]
	if strings.Contains(user, ":") {
		return nil, optionError(GitOptionURL, "URL must not carry credentials; use Token or SSHKey")
	}
	if user == "" || !isSCPWord(user) || !isSCPWord(host) {
		return nil, optionError(GitOptionURL, errURLForm)
	}
	u := &url.URL{Scheme: "ssh", User: url.User(user), Host: host, Path: path}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, optionError(GitOptionURL, "URL is not valid")
		}
		u.Host = net.JoinHostPort(host, port)
	}
	return u, nil
}

// isSCPWord reports whether s may be the user or host of an scp-like URL:
// letters, digits, '.', '_' and '-', not starting with '-', which a command
// line would read as an option.
func isSCPWord(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '.' || c == '_' || c == '-'
		if !ok || i == 0 && c == '-' {
			return false
		}
	}
	return s != ""
}
