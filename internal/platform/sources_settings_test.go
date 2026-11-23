package platform

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// #476: GOLDIE_SOURCES declares an ordered list of Sources as one JSON
// document. LoadConfig parses and validates it into Config.Sources (name,
// type, url or dir, ref, path, poll, claims, and the *_env names); a bad
// document fails startup with an error that names the variable and the entry,
// never a value. Serving several Sources is a later slice.
//
// The names are test-local so this file compiles before the implementation
// adds its Env constant.
const (
	envSources      = "GOLDIE_SOURCES"
	envPlatformTok  = "GOLDIE_SOURCE_PLATFORM_TOKEN"
	envTeamAKey     = "GOLDIE_SOURCE_TEAM_A_KEY"
	srcCanary       = "CANARY-" + canaryCore
	validSourcesDoc = `[
  {"name": "platform", "type": "git",
   "url": "https://git.example.com/platform/golden-paths.git",
   "ref": "main", "path": "content", "poll": "60s",
   "token_env": "GOLDIE_SOURCE_PLATFORM_TOKEN",
   "claims": {"global": true, "projects": ["git.example.com/platform/*"]}},
  {"name": "team-a", "type": "git",
   "url": "ssh://git@git.example.com/team-a/paths.git",
   "ssh_key_env": "GOLDIE_SOURCE_TEAM_A_KEY",
   "claims": {"projects": ["git.example.com/team-a/*"]}},
  {"name": "local", "type": "dir", "dir": "/srv/knowledge", "claims": {"global": true}}
]`
)

// srcView is one parsed Source as the tests read it. Config.Sources holds
// entries with exactly these fields (Claims as a struct with Global and
// Projects, Poll as a time.Duration); sourcesOf reads them by name so the
// tests compile before the implementation exists.
type srcView struct {
	Name, Type, URL, Dir, Ref, Path, TokenEnv, SSHKeyEnv string
	Poll                                                 time.Duration
	Global                                               bool
	Projects                                             []string
}

func sourcesOf(t *testing.T, cfg Config) []srcView {
	t.Helper()
	f := reflect.ValueOf(cfg).FieldByName("Sources")
	if !f.IsValid() || f.Kind() != reflect.Slice {
		t.Fatalf("Config has no Sources slice (#476)")
	}
	var out []srcView
	for i := range f.Len() {
		e := f.Index(i)
		str := func(name string) string {
			v := e.FieldByName(name)
			if !v.IsValid() || v.Kind() != reflect.String {
				t.Fatalf("Config.Sources entries have no string field %s (#476)", name)
			}
			return v.String()
		}
		v := srcView{Name: str("Name"), Type: str("Type"), URL: str("URL"), Dir: str("Dir"), Ref: str("Ref"),
			Path: str("Path"), TokenEnv: str("TokenEnv"), SSHKeyEnv: str("SSHKeyEnv")}
		poll := e.FieldByName("Poll")
		if !poll.IsValid() || poll.Type() != reflect.TypeOf(time.Duration(0)) {
			t.Fatalf("Config.Sources entries have no time.Duration field Poll (#476)")
		}
		v.Poll = time.Duration(poll.Int())
		c := e.FieldByName("Claims")
		if !c.IsValid() || c.Kind() != reflect.Struct || !c.FieldByName("Global").IsValid() || !c.FieldByName("Projects").IsValid() {
			t.Fatalf("Config.Sources entries have no Claims struct with Global and Projects (#476)")
		}
		v.Global = c.FieldByName("Global").Bool()
		if ps, ok := c.FieldByName("Projects").Interface().([]string); ok {
			v.Projects = ps
		} else {
			t.Fatalf("Claims.Projects is not a []string (#476)")
		}
		out = append(out, v)
	}
	return out
}

// sourcesEnv is the environment of a valid GOLDIE_SOURCES: the document and
// the variables its *_env fields name.
func sourcesEnv(doc string) map[string]string {
	return map[string]string{envSources: doc, envPlatformTok: "tok-" + canaryCore, envTeamAKey: "key-" + canaryCore}
}

// secondEntry is a document whose first entry is valid and whose second is
// the given JSON object, so an error can only name entry 1 (or 2) or its name.
func secondEntry(obj string) string {
	return `[{"name":"first","type":"dir","dir":"/srv/first","claims":{"global":true}},` + obj + `]`
}

// A valid document yields the Sources in listed order, with every field.
func TestGoldieSourcesParsesAValidDocumentInOrder(t *testing.T) {
	cfg, err := loadEnv(sourcesEnv(validSourcesDoc))
	if err != nil {
		t.Fatalf("LoadConfig with a valid %s: %v", envSources, err)
	}
	srcs := sourcesOf(t, cfg)
	if len(srcs) != 3 {
		t.Fatalf("Sources has %d entries, want 3: %+v", len(srcs), srcs)
	}
	p, a, l := srcs[0], srcs[1], srcs[2]
	if p.Name != "platform" || p.Type != "git" || p.URL != "https://git.example.com/platform/golden-paths.git" ||
		p.Ref != "main" || p.Path != "content" || p.Poll != 60*time.Second || p.Dir != "" ||
		p.TokenEnv != envPlatformTok || p.SSHKeyEnv != "" {
		t.Errorf("Sources[0] = %+v, want the platform entry as written", p)
	}
	if !p.Global || !reflect.DeepEqual(p.Projects, []string{"git.example.com/platform/*"}) {
		t.Errorf("Sources[0] claims = %+v, want global and the platform glob", []any{p.Global, p.Projects})
	}
	if a.Name != "team-a" || a.Type != "git" || a.URL != "ssh://git@git.example.com/team-a/paths.git" ||
		a.SSHKeyEnv != envTeamAKey || a.TokenEnv != "" || a.Dir != "" {
		t.Errorf("Sources[1] = %+v, want the team-a entry as written", a)
	}
	if a.Global || !reflect.DeepEqual(a.Projects, []string{"git.example.com/team-a/*"}) {
		t.Errorf("Sources[1] claims = %+v, want not global and the team-a glob", []any{a.Global, a.Projects})
	}
	if l.Name != "local" || l.Type != "dir" || l.Dir != "/srv/knowledge" || l.URL != "" || !l.Global || len(l.Projects) != 0 {
		t.Errorf("Sources[2] = %+v, want the local dir entry as written", l)
	}
}

// List order is preference order: reversing the document reverses the Sources.
func TestGoldieSourcesKeepsTheListedOrder(t *testing.T) {
	doc := `[{"name":"b","type":"dir","dir":"/b","claims":{}},{"name":"a","type":"dir","dir":"/a","claims":{}},{"name":"c","type":"dir","dir":"/c","claims":{}}]`
	cfg, err := loadEnv(map[string]string{envSources: doc})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sourcesOf(t, cfg) {
		got = append(got, s.Name)
	}
	if !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("Sources order = %v, want [b a c]", got)
	}
}

// GOLDIE_SOURCES is read through Setting, so its _FILE form works, and unset
// leaves no Sources and no error.
func TestGoldieSourcesReadsItsFileFormAndIsOptional(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.json")
	if err := os.WriteFile(path, []byte(validSourcesDoc+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := sourcesEnv("")
	delete(env, envSources)
	env[envSources+"_FILE"] = path
	cfg, err := loadEnv(env)
	if err != nil || len(sourcesOf(t, cfg)) != 3 {
		t.Errorf("%s_FILE: %d Sources, err %v; want 3 and no error", envSources, len(sourcesOf(t, cfg)), err)
	}
	cfg, err = loadEnv(nil)
	if err != nil || len(sourcesOf(t, cfg)) != 0 {
		t.Errorf("unset: %d Sources, err %v; want none and no error", len(sourcesOf(t, cfg)), err)
	}
	both := sourcesEnv(validSourcesDoc)
	both[envSources+"_FILE"] = path
	if _, err := loadEnv(both); err == nil || !strings.Contains(err.Error(), envSources+" and "+envSources+"_FILE") {
		t.Errorf("value and _FILE: err = %v, want the Setting both-set error", err)
	}
}

// A *_env field names an environment variable, read through Setting: its
// _FILE form counts as set, and the value never reaches the Sources or an error.
func TestGoldieSourcesEnvFieldsAreReadThroughSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(path, []byte("tok-"+canaryCore+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := `[{"name":"a","type":"git","url":"https://git.example.com/a.git","token_env":"GOLDIE_SOURCE_A_TOKEN","claims":{}}]`
	cfg, err := loadEnv(map[string]string{envSources: doc, "GOLDIE_SOURCE_A_TOKEN_FILE": path})
	if err != nil {
		t.Fatalf("token_env set through its _FILE form: %v", err)
	}
	if got := sourcesOf(t, cfg); len(got) != 1 || got[0].TokenEnv != "GOLDIE_SOURCE_A_TOKEN" {
		t.Errorf("Sources = %+v, want token_env kept as the variable's name", got)
	}
	if _, err := loadEnv(map[string]string{envSources: doc, "GOLDIE_SOURCE_A_TOKEN": "x", "GOLDIE_SOURCE_A_TOKEN_FILE": path}); err == nil ||
		!strings.Contains(err.Error(), "GOLDIE_SOURCE_A_TOKEN") || strings.Contains(err.Error(), canaryCore) {
		t.Errorf("token_env both forms: err = %v, want the Setting error naming it and no value", err)
	}
}

// entryRef matches an error's mention of entry 1 or 2 by index, in any of the
// usual spellings (0- or 1-based), when it does not name the entry.
var entryRef = regexp.MustCompile(`(^|[^0-9a-zA-Z])[0-2]([^0-9a-zA-Z]|$)`)

// Each bad document fails startup with an error that names GOLDIE_SOURCES and
// the entry (its index or its name) and echoes no value, URL or path.
func TestGoldieSourcesRejectsABadDocumentNamingTheVariableAndTheEntry(t *testing.T) {
	long := strings.Repeat("a", 64)
	cases := map[string]struct {
		doc  string
		name string // the bad entry's name, when it is a safe one to look for
	}{
		"unknown key":          {secondEntry(`{"name":"second","type":"dir","dir":"/srv/x","claims":{},"` + srcCanary + `":1}`), "second"},
		"unknown claims key":   {secondEntry(`{"name":"second","type":"dir","dir":"/srv/x","claims":{"` + srcCanary + `":true}}`), "second"},
		"duplicate name":       {secondEntry(`{"name":"first","type":"dir","dir":"/srv/` + srcCanary + `","claims":{}}`), "first"},
		"name underscore":      {secondEntry(`{"name":"bad_name-` + srcCanary + `","type":"dir","dir":"/srv/x","claims":{}}`), ""},
		"name leading hyphen":  {secondEntry(`{"name":"-` + srcCanary + `","type":"dir","dir":"/srv/x","claims":{}}`), ""},
		"name empty":           {secondEntry(`{"name":"","type":"dir","dir":"/srv/` + srcCanary + `","claims":{}}`), ""},
		"name missing":         {secondEntry(`{"type":"dir","dir":"/srv/` + srcCanary + `","claims":{}}`), ""},
		"name too long":        {secondEntry(`{"name":"` + long + `","type":"dir","dir":"/srv/` + srcCanary + `","claims":{}}`), ""},
		"name not ascii":       {secondEntry(`{"name":"café-` + srcCanary + `","type":"dir","dir":"/srv/x","claims":{}}`), ""},
		"neither url nor dir":  {secondEntry(`{"name":"second","type":"dir","path":"` + srcCanary + `","claims":{}}`), "second"},
		"both url and dir":     {secondEntry(`{"name":"second","type":"git","url":"https://u:` + srcCanary + `@git.example.com/a.git","dir":"/srv/` + srcCanary + `","claims":{}}`), "second"},
		"type upstream":        {secondEntry(`{"name":"second","type":"upstream","url":"https://git.example.com/` + srcCanary + `.git","claims":{}}`), "second"},
		"type empty":           {secondEntry(`{"name":"second","type":"","dir":"/srv/` + srcCanary + `","claims":{}}`), "second"},
		"type uppercase":       {secondEntry(`{"name":"second","type":"GIT","url":"https://git.example.com/` + srcCanary + `.git","claims":{}}`), "second"},
		"git type with dir":    {secondEntry(`{"name":"second","type":"git","dir":"/srv/` + srcCanary + `","claims":{}}`), "second"},
		"dir type with url":    {secondEntry(`{"name":"second","type":"dir","url":"https://git.example.com/` + srcCanary + `.git","claims":{}}`), "second"},
		"poll below floor":     {secondEntry(`{"name":"second","type":"git","url":"https://git.example.com/` + srcCanary + `.git","poll":"9s","claims":{}}`), "second"},
		"poll not a duration":  {secondEntry(`{"name":"second","type":"git","url":"https://git.example.com/a.git","poll":"` + srcCanary + `","claims":{}}`), "second"},
		"poll zero":            {secondEntry(`{"name":"second","type":"git","url":"https://git.example.com/` + srcCanary + `.git","poll":"0s","claims":{}}`), "second"},
		"poll negative":        {secondEntry(`{"name":"second","type":"git","url":"https://git.example.com/` + srcCanary + `.git","poll":"-1m","claims":{}}`), "second"},
		"project glob":         {secondEntry(`{"name":"second","type":"dir","dir":"/srv/x","claims":{"projects":["git.example.com/[` + srcCanary + `"]}}`), "second"},
		"token_env unset":      {secondEntry(`{"name":"second","type":"git","url":"https://git.example.com/a.git","token_env":"GOLDIE_UNSET_` + srcCanary + `","claims":{}}`), "second"},
		"ssh_key_env unset":    {secondEntry(`{"name":"second","type":"git","url":"ssh://git@git.example.com/a.git","ssh_key_env":"GOLDIE_UNSET_` + srcCanary + `","claims":{}}`), "second"},
		"entry not an object":  {secondEntry(`"` + srcCanary + `"`), ""},
		"name not a string":    {secondEntry(`{"name":7,"type":"dir","dir":"/srv/` + srcCanary + `","claims":{}}`), ""},
		"claims not an object": {secondEntry(`{"name":"second","type":"dir","dir":"/srv/x","claims":"` + srcCanary + `"}`), "second"},
	}
	for form, c := range cases {
		_, err := loadEnv(sourcesEnv(c.doc))
		if err == nil {
			t.Errorf("%s: LoadConfig accepted the document", form)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, envSources) {
			t.Errorf("%s: err = %v, want it to name %s", form, err, envSources)
		}
		if (c.name == "" || !strings.Contains(msg, c.name)) && !entryRef.MatchString(strings.ReplaceAll(msg, envSources, "")) {
			t.Errorf("%s: err = %v, want it to name the entry by index or name", form, err)
		}
		for _, hidden := range []string{srcCanary, canaryCore, "git.example.com", "/srv/"} {
			if strings.Contains(msg, hidden) {
				t.Errorf("%s: err = %v echoes a value (%q)", form, err, hidden)
			}
		}
	}
}

// A document that is not a usable list fails naming GOLDIE_SOURCES and echoing
// nothing: an empty list, not JSON, not a list, trailing data.
func TestGoldieSourcesRejectsAnEmptyOrMalformedDocument(t *testing.T) {
	for form, doc := range map[string]string{
		"empty list":    `[]`,
		"spaced empty":  " [ ] \n",
		"null":          `null`,
		"not JSON":      `platform-` + srcCanary,
		"object":        `{"name":"a","type":"dir","dir":"/srv/` + srcCanary + `"}`,
		"trailing data": validSourcesDoc + ` ` + srcCanary,
		"truncated":     `[{"name":"a","type":"dir","dir":"/srv/` + srcCanary,
	} {
		_, err := loadEnv(sourcesEnv(doc))
		if err == nil {
			t.Errorf("%s: LoadConfig accepted %s", form, envSources)
			continue
		}
		if !strings.Contains(err.Error(), envSources) {
			t.Errorf("%s: err = %v, want it to name %s", form, err, envSources)
		}
		for _, hidden := range []string{srcCanary, canaryCore, "/srv/"} {
			if strings.Contains(err.Error(), hidden) {
				t.Errorf("%s: err = %v echoes the document (%q)", form, err, hidden)
			}
		}
	}
}

// The poll floor is the existing one: exactly 10s is accepted, and a source
// without poll is accepted (it follows GOLDIE_POLL_INTERVAL).
func TestGoldieSourcesPollAtTheFloorIsAccepted(t *testing.T) {
	doc := `[{"name":"a","type":"git","url":"https://git.example.com/a.git","poll":"10s","claims":{}},{"name":"b","type":"git","url":"https://git.example.com/b.git","claims":{}}]`
	cfg, err := loadEnv(map[string]string{envSources: doc})
	if err != nil {
		t.Fatalf("poll 10s: %v", err)
	}
	if got := sourcesOf(t, cfg); len(got) != 2 || got[0].Poll != MinPollInterval {
		t.Errorf("Sources = %+v, want poll 10s on the first", got)
	}
}

// GOLDIE_SOURCES together with GOLDIE_GIT_URL or GOLDIE_CONTENT_DIR is an
// error naming both variables and echoing neither value, in the same error as
// any other problem; neither alone, nor an empty one, conflicts.
func TestGoldieSourcesExcludesTheSingleSourceVariables(t *testing.T) {
	const url = "https://deploy:pw-" + canaryCore + "@git.example.com/org/content.git"
	dirValue := "/srv/content-" + canaryCore[:8]
	for _, other := range []struct{ name, value string }{{EnvGitURL, url}, {EnvContentDir, dirValue}} {
		env := sourcesEnv(validSourcesDoc)
		env[other.name] = other.value
		_, err := loadEnv(env)
		if err == nil {
			t.Errorf("%s with %s: LoadConfig accepted both", envSources, other.name)
			continue
		}
		for _, name := range []string{envSources, other.name} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("%s with %s: err = %v, want it to name %s", envSources, other.name, err, name)
			}
		}
		for _, hidden := range []string{canaryCore, canaryCore[:8], "deploy", "git.example.com", "/srv/"} {
			if strings.Contains(err.Error(), hidden) {
				t.Errorf("%s with %s: err = %v echoes a value (%q)", envSources, other.name, err, hidden)
			}
		}
	}
	file := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(file, []byte(url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := sourcesEnv(validSourcesDoc)
	env[EnvGitURL+"_FILE"] = file
	if _, err := loadEnv(env); err == nil || !strings.Contains(err.Error(), EnvGitURL) {
		t.Errorf("%s with %s_FILE: err = %v, want the conflict", envSources, EnvGitURL, err)
	}
	env = sourcesEnv(validSourcesDoc)
	env[EnvLogLevel] = "loud"
	env[EnvGitURL] = url
	if _, err := loadEnv(env); err == nil || !strings.Contains(err.Error(), EnvLogLevel) || !strings.Contains(err.Error(), envSources) {
		t.Errorf("err = %v, want the conflict and the bad log level in one error", err)
	}
	for form, env := range map[string]map[string]string{
		"empty git url":     {envSources: validSourcesDoc, EnvGitURL: "", envPlatformTok: "t", envTeamAKey: "k"},
		"empty content dir": {envSources: validSourcesDoc, EnvContentDir: "", envPlatformTok: "t", envTeamAKey: "k"},
	} {
		if _, err := loadEnv(env); err != nil {
			t.Errorf("%s: %v, want it accepted", form, err)
		}
	}
}

// The configuration reference has the GOLDIE_SOURCES row, generated from the
// registry (./run regen), with the example document.
func TestGoldieSourcesIsDocumentedWithTheExample(t *testing.T) {
	s := settingRow(t, envSources)
	if s.Secret {
		t.Errorf("%s is marked Secret; the document holds no secret (*_env fields only name variables)", envSources)
	}
	ref := committedReference(t)
	if !strings.Contains(ref, "\n| `"+envSources+"` | ") {
		t.Errorf("committed docs/reference/configuration.md has no row for %s (run: ./run regen)", envSources)
	}
	for _, want := range []string{"git.example.com/platform/golden-paths.git", envPlatformTok, envTeamAKey, "/srv/knowledge", "team-a"} {
		if !strings.Contains(ref, want) {
			t.Errorf("configuration reference lacks the example's %q", want)
		}
	}
}
