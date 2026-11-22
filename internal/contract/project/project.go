// Package project detects the Project a working directory belongs to. The CLI
// and the MCP stdio role both use it, so that one parser reads the untrusted
// .git/config and .goldie/config.yaml of a checkout.
//
// Detect detects the Project, trying in order: the flag value, the environment
// value, .goldie/config.yaml, then the git remotes upstream and origin. The
// package owns detection only and words nothing for the user: callers map a
// Source, and the Source of a SourceError, to their own text. It imports no
// other goldie module except contract.
package project

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/thschue/goldie/internal/contract"
)

// Source says how a Project was found, in the order of precedence. The zero
// Source means none was found.
type Source string

// The ways a Project is found.
const (
	SourceFlag     Source = "flag"
	SourceEnv      Source = "env"
	SourceConfig   Source = "config"
	SourceUpstream Source = "upstream"
	SourceOrigin   Source = "origin"
)

// SourceError says that the Project a user gave through Source is unusable.
// Err is the domain reason and holds no part of the value, which may carry a
// credential. The caller words the Source, for example as a prefix.
type SourceError struct {
	Source Source
	Err    error
}

func (e *SourceError) Error() string { return e.Err.Error() }

func (e *SourceError) Unwrap() error { return e.Err }

// maxProjectFile bounds every file the detection reads. Both .git/config and
// .goldie/config.yaml are untrusted input from the checked-out repository.
const maxProjectFile = 1 << 20

// maxWalkUp bounds how many parent directories the detection visits.
const maxWalkUp = 64

// Detected is a detected or configured Project and where it came from.
type Detected struct {
	ID     string // host/org/repo, or the value given by the user
	Source Source // how it was found; the zero value when none was

	// Rejected names the remote (upstream or origin) that exists but is not
	// in the host/org/repo form, when nothing else gave a Project. It is the
	// name only, never the URL, which may hold a credential or a private path.
	Rejected string
}

// Detect detects the Project, trying in order: --project, GOLDIE_PROJECT,
// .goldie/config.yaml, then the git remotes `upstream` and `origin`. dir is
// the working directory. It returns the zero Project and no error when
// nothing names one, for example outside a git repository. A broken
// .goldie/config.yaml is an error: the user wrote it to be obeyed.
func Detect(dir, flagValue, envValue string) (Detected, error) {
	for _, c := range []struct {
		v      string
		source Source
	}{{flagValue, SourceFlag}, {envValue, SourceEnv}} {
		if v := strings.TrimSpace(c.v); v != "" {
			id, err := normaliseConfigured(v)
			if err != nil {
				return Detected{}, &SourceError{Source: c.source, Err: err}
			}
			return Detected{ID: id, Source: c.source}, nil
		}
	}
	root, gitConfig, inRepo := findGitConfig(dir)
	if !inRepo {
		root = dir // outside a repository only the working directory counts
	}
	id, err := projectFromConfigFile(dir, root)
	if err != nil {
		return Detected{}, err
	}
	if id != "" {
		return Detected{ID: id, Source: SourceConfig}, nil
	}
	return projectFromRemotes(gitConfig), nil
}

// normaliseConfigured normalises a Project the user typed: a remote URL
// becomes host/org/repo, anything else is lower-cased.
//
// Every Project the clients send passes contract.ValidProjectID, the ProjectId
// schema of the spec (ADR 0020), after lower-casing. It keeps nested groups,
// '..', percent escapes, query and fragment characters and other path syntax
// from a hostile remote out of the Project, and the server accepts what it
// passes.
func normaliseConfigured(v string) (string, error) {
	if id, ok := normaliseRemote(v); ok {
		return id, nil
	}
	// Anything that looks like a URL or carries userinfo but cannot be read is
	// refused without echoing it: it may hold a credential.
	if strings.ContainsAny(v, "@:") || strings.ContainsFunc(v, contract.HiddenRune) {
		return "", errors.New("the Project is not a host/org/repo name or a git remote URL")
	}
	// A plain host/org/repo gets the same shape as a detected one.
	id := strings.ToLower(strings.TrimSuffix(strings.Trim(v, "/"), ".git"))
	if !contract.ValidProjectID(id) {
		return "", errors.New("the Project is not host/org/repo: a lower-case host, then an org and a repo of letters, digits, '.', '_' and '-'")
	}
	return id, nil
}

// projectFromConfigFile reads `project:` from the nearest .goldie/config.yaml
// from dir up to root, the repository root. It never looks above root: a
// repository uses its own .goldie/, not one of a checkout it is nested in.
func projectFromConfigFile(dir, root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", nil
	}
	all, dirs := ancestors(dir), []string(nil)
	for i, d := range all {
		if d == absRoot {
			dirs = all[:i+1]
			break
		}
	}
	for _, d := range dirs {
		b, err := readCapped(filepath.Join(d, ".goldie", "config.yaml"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", errors.New("cannot read .goldie/config.yaml")
		}
		var cfg struct {
			Project string `yaml:"project"`
		}
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return "", errors.New(".goldie/config.yaml is not valid YAML with a string `project:` key")
		}
		if strings.TrimSpace(cfg.Project) == "" {
			return "", nil // no `project:` key: detection decides
		}
		id, err := normaliseConfigured(strings.TrimSpace(cfg.Project))
		if err != nil {
			return "", &SourceError{Source: SourceConfig, Err: err}
		}
		return id, nil
	}
	return "", nil
}

// projectFromRemotes detects the Project from the `upstream` remote, else
// `origin`, of a git config's text.
func projectFromRemotes(cfg string) Detected {
	urls := remoteURLs(cfg)
	rejected := ""
	for _, r := range []struct {
		name   string
		source Source
	}{
		{"upstream", SourceUpstream},
		{"origin", SourceOrigin},
	} {
		if id, ok := normaliseRemote(urls[r.name]); ok {
			return Detected{ID: id, Source: r.source}
		}
		if rejected == "" && strings.TrimSpace(urls[r.name]) != "" {
			rejected = r.name
		}
	}
	return Detected{Rejected: rejected}
}

// ancestors lists dir and its parents, nearest first.
func ancestors(dir string) []string {
	var out []string
	d, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	for i := 0; i < maxWalkUp; i++ {
		out = append(out, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return out
}

// errNotRegular refuses a FIFO, device or socket, which could block the read.
var errNotRegular = errors.New("not a regular file")

// errFileTooLarge refuses a Project file above maxProjectFile.
var errFileTooLarge = errors.New("file too large")

// readCapped reads a regular file of at most maxProjectFile bytes. It checks
// the type before opening, because opening a FIFO (or a symlink to one, or to
// /dev/tty) planted in a checkout blocks, and so that no device is opened.
func readCapped(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return openCapped(path)
}

// openCapped opens path without blocking (openFlags) and checks the type of
// what it opened, so a FIFO swapped in after readCapped's check is refused
// rather than read.
func openCapped(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	b, err := io.ReadAll(io.LimitReader(f, maxProjectFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxProjectFile {
		return nil, errFileTooLarge
	}
	return b, nil
}

// findGitConfig walks up from dir to the repository root, the nearest
// directory with a `.git`, and returns it and the text of its config (empty
// when it cannot be read). inRepo is false when no `.git` is found. A `.git`
// directory holds the config itself. A `.git` file (worktree or submodule)
// points at a gitdir; a worktree's gitdir names the main repository's
// directory in `commondir`, where the shared config lives.
func findGitConfig(dir string) (root, cfg string, inRepo bool) {
	for _, d := range ancestors(dir) {
		dotGit := filepath.Join(d, ".git")
		fi, err := os.Stat(dotGit)
		if err != nil {
			continue
		}
		gitDir := dotGit
		if !fi.IsDir() {
			b, err := readCapped(dotGit)
			if err != nil {
				return d, "", true
			}
			target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !ok {
				return d, "", true
			}
			gitDir = resolveFrom(d, strings.TrimSpace(target))
		}
		if b, err := readCapped(filepath.Join(gitDir, "commondir")); err == nil {
			gitDir = resolveFrom(gitDir, strings.TrimSpace(string(b)))
		}
		b, err := readCapped(filepath.Join(gitDir, "config"))
		if err != nil {
			return d, "", true
		}
		return d, string(b), true
	}
	return "", "", false
}

func resolveFrom(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// remoteURLs returns the first `url` of every `[remote "name"]` section of a
// git config. It reads only what detection needs: it ignores includes and
// url.<base>.insteadOf rewrites.
func remoteURLs(cfg string) map[string]string {
	urls := map[string]string{}
	section := ""
	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			section = remoteSection(line)
			continue
		}
		if section == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		if _, seen := urls[section]; !seen {
			urls[section] = unquote(strings.TrimSpace(val))
		}
	}
	return urls
}

// remoteSection returns the remote name of a `[remote "name"]` header, or ""
// for any other section.
func remoteSection(header string) string {
	end := strings.LastIndex(header, "]")
	if end < 0 {
		return ""
	}
	name, rest, ok := strings.Cut(strings.TrimSpace(header[1:end]), " ")
	if !ok || !strings.EqualFold(name, "remote") {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return ""
	}
	return rest[1 : len(rest)-1]
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// normaliseRemote turns a git remote URL into host/org/repo: lower case,
// without scheme, userinfo, port, `.git` suffix or trailing slash. It accepts
// the scp form (git@host:org/repo.git), ssh://, git://, http:// and https://.
// Userinfo is dropped so a token in a remote URL never reaches the output. ok
// is false for a local path or anything it cannot read as host plus path.
func normaliseRemote(raw string) (id string, ok bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "ssh", "git", "http", "https", "git+ssh", "ssh+git":
		default:
			return "", false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp form: [user@]host:path, where the colon comes before any slash.
		colon, slash := strings.Index(raw, ":"), strings.Index(raw, "/")
		if colon <= 0 || (slash >= 0 && slash < colon) {
			return "", false
		}
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		host = strings.Trim(host, "[]")
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if host == "" || path == "" || strings.ContainsAny(host, " \t/@") || strings.ContainsAny(path, "@ \t") || strings.Contains(path, "//") {
		return "", false
	}
	id = strings.ToLower(host + "/" + path)
	if !contract.ValidProjectID(id) { // its ASCII-only rule also keeps out control and bidi runes
		return "", false
	}
	return id, true
}
