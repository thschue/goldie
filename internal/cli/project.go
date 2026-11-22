package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// How a Project was found, in the order of precedence. These are the words
// `goldie status` prints and the JSON "projectSource" carries.
const (
	SourceFlag     = "--project"
	SourceEnv      = EnvProject
	SourceConfig   = ".goldie/config.yaml"
	SourceUpstream = "git remote upstream"
	SourceOrigin   = "git remote origin"
)

// maxProjectFile bounds every file the detection reads. Both .git/config and
// .goldie/config.yaml are untrusted input from the checked-out repository.
const maxProjectFile = 1 << 20

// maxWalkUp bounds how many parent directories the detection visits.
const maxWalkUp = 64

// Project is a detected or configured Project and where it came from.
type Project struct {
	ID     string // host/org/repo, or the value given by the user
	Source string // one of the Source constants
}

// ResolveProject applies the override order: --project, GOLDIE_PROJECT,
// .goldie/config.yaml, then the git remotes `upstream` and `origin`. dir is
// the working directory. It returns the zero Project and no error when
// nothing names one, for example outside a git repository. A broken
// .goldie/config.yaml is an error: the user wrote it to be obeyed.
func ResolveProject(dir, flagValue, envValue string) (Project, error) {
	for _, c := range []struct{ v, source string }{{flagValue, SourceFlag}, {envValue, SourceEnv}} {
		if v := strings.TrimSpace(c.v); v != "" {
			id, err := normaliseConfigured(v)
			if err != nil {
				return Project{}, fmt.Errorf("%s: %w", c.source, err)
			}
			return Project{ID: id, Source: c.source}, nil
		}
	}
	id, err := projectFromConfigFile(dir)
	if err != nil {
		return Project{}, err
	}
	if id != "" {
		return Project{ID: id, Source: SourceConfig}, nil
	}
	return projectFromGit(dir), nil
}

// normaliseConfigured normalises a Project the user typed: a remote URL
// becomes host/org/repo, anything else is kept as written.
func normaliseConfigured(v string) (string, error) {
	if id, ok := NormaliseRemote(v); ok {
		return id, nil
	}
	// Anything that looks like a URL or carries userinfo but cannot be read is
	// refused without echoing it: it may hold a credential.
	if strings.ContainsAny(v, "@:") || strings.ContainsFunc(v, unsafe) {
		return "", errors.New("the Project is not a host/org/repo name or a git remote URL")
	}
	// A plain host/org/repo gets the same shape as a detected one.
	return strings.ToLower(strings.TrimSuffix(strings.Trim(v, "/"), ".git")), nil
}

// projectFromConfigFile reads `project:` from the nearest .goldie/config.yaml
// at or above dir.
func projectFromConfigFile(dir string) (string, error) {
	for _, d := range ancestors(dir) {
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
		id, err := normaliseConfigured(strings.TrimSpace(cfg.Project))
		if err != nil {
			return "", fmt.Errorf("%s: %w", SourceConfig, err)
		}
		return id, nil
	}
	return "", nil
}

// projectFromGit detects the Project from the `upstream` remote, else
// `origin`, reading .git/config without running git.
func projectFromGit(dir string) Project {
	cfg, ok := findGitConfig(dir)
	if !ok {
		return Project{}
	}
	urls := remoteURLs(cfg)
	for _, r := range []struct{ name, source string }{
		{"upstream", SourceUpstream},
		{"origin", SourceOrigin},
	} {
		if id, ok := NormaliseRemote(urls[r.name]); ok {
			return Project{ID: id, Source: r.source}
		}
	}
	return Project{}
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

// readCapped reads a file of at most maxProjectFile bytes.
func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxProjectFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxProjectFile {
		return nil, errTooLarge
	}
	return b, nil
}

// findGitConfig walks up from dir to the repository and returns the text of
// its config. A `.git` directory holds the config itself. A `.git` file
// (worktree or submodule) points at a gitdir; a worktree's gitdir names the
// main repository's directory in `commondir`, where the shared config lives.
func findGitConfig(dir string) (string, bool) {
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
				return "", false
			}
			target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !ok {
				return "", false
			}
			gitDir = resolveFrom(d, strings.TrimSpace(target))
		}
		if b, err := readCapped(filepath.Join(gitDir, "commondir")); err == nil {
			gitDir = resolveFrom(gitDir, strings.TrimSpace(string(b)))
		}
		b, err := readCapped(filepath.Join(gitDir, "config"))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return "", false
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

// NormaliseRemote turns a git remote URL into host/org/repo: lower case,
// without scheme, userinfo, port, `.git` suffix or trailing slash. It accepts
// the scp form (git@host:org/repo.git), ssh://, git://, http:// and https://.
// Userinfo is dropped so a token in a remote URL never reaches the output. ok
// is false for a local path or anything it cannot read as host plus path.
func NormaliseRemote(raw string) (id string, ok bool) {
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
	if strings.IndexFunc(id, func(r rune) bool { return unsafe(r) || r == ' ' }) >= 0 {
		return "", false
	}
	return id, true
}

// describe is the status line for a Project.
func (p Project) describe() string {
	if p.ID == "" {
		return "none found (set --project, " + EnvProject + " or .goldie/config.yaml, or run inside a git repository with an upstream or origin remote)"
	}
	return fmt.Sprintf("%s (from %s)", cleanLine(p.ID), p.Source)
}
