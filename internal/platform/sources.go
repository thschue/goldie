package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

// SourceSpec is one entry of GOLDIE_SOURCES, validated. The *_env fields name
// environment variables; the secrets themselves are never in the document.
type SourceSpec struct {
	Name      string // a DNS label, unique in the list
	Type      string // "git" or "dir"
	URL       string // git only
	Dir       string // dir only
	Ref       string
	Path      string
	Poll      time.Duration // zero follows GOLDIE_POLL_INTERVAL
	TokenEnv  string
	SSHKeyEnv string
	Claims    SourceClaims
}

// SourceClaims says which Projects a Source answers for. Matching is #477.
type SourceClaims struct {
	Global   bool
	Projects []string // globs, each valid for path.Match
}

// sourceEntry is the JSON shape of one entry.
type sourceEntry struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	Dir       string `json:"dir"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	Poll      string `json:"poll"`
	TokenEnv  string `json:"token_env"`
	SSHKeyEnv string `json:"ssh_key_env"`
	Claims    struct {
		Global   bool     `json:"global"`
		Projects []string `json:"projects"`
	} `json:"claims"`
}

// ParseSources parses and validates a GOLDIE_SOURCES document. Every error
// names the variable and the entry (its 1-based position, and its name when
// that is a DNS label) and never a value: the document may hold a URL with a
// password, and encoding/json's own errors quote values and keys, so none is
// passed on. The *_env variables are read through Setting, so NAME_FILE works.
func ParseSources(doc string, lookup Lookup, readFile func(string) ([]byte, error)) ([]SourceSpec, error) {
	var raws []json.RawMessage
	dec := json.NewDecoder(strings.NewReader(doc))
	if err := dec.Decode(&raws); err != nil {
		return nil, fmt.Errorf("%s: must be a JSON list of Sources", EnvSources)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: must be one JSON list, with nothing after it", EnvSources)
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("%s: the list is empty: declare at least one Source", EnvSources)
	}
	var errs []error
	var out []SourceSpec
	seen := map[string]bool{}
	for i, raw := range raws {
		label := "entry " + strconv.Itoa(i+1)
		var e sourceEntry
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %s is not an object with known keys of the right types", EnvSources, label))
			continue
		}
		if IsDNSLabel(e.Name) {
			label += " (" + e.Name + ")"
		}
		s, problems := validateSource(e, lookup, readFile)
		if s.Name != "" && seen[s.Name] {
			problems = append(problems, "name is used by an earlier entry")
		}
		seen[s.Name] = true
		for _, p := range problems {
			errs = append(errs, fmt.Errorf("%s: %s: %s", EnvSources, label, p))
		}
		out = append(out, s)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// validateSource checks one entry and returns it with its problems, as fixed
// texts that never echo a value.
func validateSource(e sourceEntry, lookup Lookup, readFile func(string) ([]byte, error)) (SourceSpec, []string) {
	var p []string
	s := SourceSpec{Name: e.Name, Type: e.Type, URL: e.URL, Dir: e.Dir, Ref: e.Ref, Path: e.Path,
		TokenEnv: e.TokenEnv, SSHKeyEnv: e.SSHKeyEnv,
		Claims: SourceClaims{Global: e.Claims.Global, Projects: e.Claims.Projects}}
	if !IsDNSLabel(e.Name) {
		p = append(p, "name must be a DNS label: 1 to 63 ASCII letters, digits and hyphens, not starting or ending with a hyphen")
	}
	switch e.Type {
	case "git":
		if e.URL == "" || e.Dir != "" {
			p = append(p, "a git Source needs url and no dir")
		}
	case "dir":
		if e.Dir == "" || e.URL != "" {
			p = append(p, "a dir Source needs dir and no url")
		}
	default:
		p = append(p, "type must be git or dir")
	}
	if e.Poll != "" {
		if d, err := time.ParseDuration(e.Poll); err != nil || d < PollIntervalFloor() {
			p = append(p, "poll must be a Go duration of at least "+PollIntervalFloor().String())
		} else {
			s.Poll = d
		}
	}
	for _, g := range e.Claims.Projects {
		if _, err := path.Match(g, ""); err != nil {
			p = append(p, "claims.projects has a glob that does not parse")
			break
		}
	}
	for _, f := range []struct{ key, env string }{{"token_env", e.TokenEnv}, {"ssh_key_env", e.SSHKeyEnv}} {
		if f.env == "" {
			continue
		}
		if !strings.HasPrefix(f.env, "GOLDIE_") {
			p = append(p, f.key+" must name a GOLDIE_-prefixed variable")
			continue
		}
		v, err := Setting(lookup, readFile, f.env)
		if err != nil {
			p = append(p, f.key+": "+err.Error())
		} else if v == "" {
			p = append(p, f.key+" names a variable that is not set")
		}
	}
	return s, p
}

// sourceNames is the startup report's form of the list: names only, in order.
func sourceNames(srcs []SourceSpec) string {
	names := make([]string, len(srcs))
	for i, s := range srcs {
		names[i] = s.Name
	}
	return strings.Join(names, ",")
}
