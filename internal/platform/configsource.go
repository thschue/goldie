package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// The places a setting's value comes from, in precedence order, then the
// fallback. The startup report and the configuration errors name them.
const (
	SourceFlag        = "flag"
	SourceEnvironment = "environment"
	SourceFile        = "file"
	SourceDefault     = "default"
)

// settingKey is a setting's key in the config file: its variable name without
// GOLDIE_, in lower case (GOLDIE_POLL_INTERVAL is poll_interval).
func settingKey(name string) string { return strings.ToLower(strings.TrimPrefix(name, "GOLDIE_")) }

// settingFlag is a setting's flag name: its key with hyphens (--poll-interval).
func settingFlag(name string) string { return strings.ReplaceAll(settingKey(name), "_", "-") }

// AddConfigFlags registers one flag per Settings row on fs. A secret has no
// flag of its own, because a flag shows in the process list: its --x-file form
// names the file that holds it, and a hidden --x flag exists only so that
// NewLayers can refuse it by name. Every flag takes text: Changed tells a
// value that was given from one that was not, and LoadConfig checks the text
// as it does for the environment.
func AddConfigFlags(fs *pflag.FlagSet) {
	fs.String(settingFlag(EnvConfig), "", "YAML config file the settings may come from ("+EnvConfig+")")
	for _, s := range Settings {
		name := settingFlag(s.Name)
		usage := strings.ReplaceAll(firstSentence(s.Meaning), "`", "'")
		if !s.Secret {
			fs.String(name, "", usage)
			continue
		}
		fs.String(name, "", "")
		_ = fs.MarkHidden(name)
		fs.String(name+"-file", "", "file that holds "+s.Name+": "+usage)
	}
}

// firstSentence is text up to its first full stop followed by a space.
func firstSentence(text string) string {
	if i := strings.Index(text, ". "); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSuffix(text, ".")
}

// Layers resolves each setting from, in order, a flag, the environment, the
// config file and finally the default that LoadConfig applies. It answers
// LoadConfig's Lookup, so validation, _FILE handling and redaction stay in
// LoadConfig, and it knows which layer answered.
type Layers struct {
	flags *pflag.FlagSet
	env   Lookup
	file  map[string]string // config file key -> text
}

// NewLayers reads the config file named by --config or GOLDIE_CONFIG, if any.
// It refuses a secret given as a flag, a secret held inline in the file and a
// key that is no setting. Every error names the flag or key and never a value.
func NewLayers(fs *pflag.FlagSet, env Lookup, readFile func(string) ([]byte, error)) (*Layers, error) {
	l := &Layers{flags: fs, env: env, file: map[string]string{}}
	var errs []error
	for _, s := range Settings {
		if s.Secret && fs.Changed(settingFlag(s.Name)) {
			errs = append(errs, fmt.Errorf("--%s: a secret is never given as a flag, it would show in the process list: use --%s-file", settingFlag(s.Name), settingFlag(s.Name)))
		}
	}
	path, ok := "", false
	if fs.Changed(settingFlag(EnvConfig)) {
		path, ok = fs.Lookup(settingFlag(EnvConfig)).Value.String(), true
	} else if v, set := env(EnvConfig); set {
		path, ok = v, true
	}
	if ok {
		if path == "" {
			errs = append(errs, errors.New("config: the config file path is empty"))
		} else if err := l.readFile(path, readFile); err != nil {
			errs = append(errs, err)
		}
	}
	return l, errors.Join(errs...)
}

// readFile loads the YAML config file into l.file. viper parses the file.
func (l *Layers) readFile(path string, readFile func(string) ([]byte, error)) error {
	b, err := readFile(path)
	if err != nil {
		return errors.New("config: cannot read the config file")
	}
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(b)); err != nil {
		return errors.New("config: the config file is not valid YAML")
	}
	known := map[string]bool{}
	secret := map[string]bool{}
	for _, s := range Settings {
		key := settingKey(s.Name)
		if s.Secret {
			secret[key] = true
			known[key+"_file"] = true
		} else {
			known[key] = true
		}
	}
	all := v.AllSettings()
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, k := range keys {
		switch {
		case secret[k]:
			errs = append(errs, fmt.Errorf("%s: a secret is never set inline in the config file: use %s_file", k, k))
		case !known[k]:
			errs = append(errs, fmt.Errorf("%q: not a setting, so the config file is refused", k))
		default:
			text, ok, err := fileText(k, all[k])
			if err != nil {
				errs = append(errs, err)
			} else if ok {
				l.file[k] = text
			}
		}
	}
	return errors.Join(errs...)
}

// fileText is the text LoadConfig reads for a config file value: scalars as
// written, a list of scalars comma-joined as the variables take it, and the
// sources list as the JSON document of GOLDIE_SOURCES. ok is false for an
// empty (null) value, which is as good as absent.
func fileText(key string, v any) (text string, ok bool, err error) {
	switch x := v.(type) {
	case nil:
		return "", false, nil
	case string:
		return x, true, nil
	case []any:
		if key == settingKey(EnvSources) {
			doc, err := json.Marshal(x)
			if err != nil {
				return "", false, fmt.Errorf("%s: cannot be read as a list of Sources", key)
			}
			return string(doc), true, nil
		}
		parts := make([]string, len(x))
		for i, e := range x {
			switch e.(type) {
			case map[string]any, []any, nil:
				return "", false, fmt.Errorf("%s: must be a value or a list of values", key)
			}
			parts[i] = fmt.Sprint(e)
		}
		return strings.Join(parts, ","), true, nil
	case map[string]any:
		return "", false, fmt.Errorf("%s: must be a value or a list of values", key)
	}
	return fmt.Sprint(v), true, nil
}

// spec reports whether name is a Settings row, and whether it is a secret.
func spec(name string) (found, secret bool) {
	for _, s := range Settings {
		if s.Name == name {
			return true, s.Secret
		}
	}
	return false, false
}

// Source names the layer that gives setting name (a Settings row's variable
// name): the highest of flag, environment and file that sets it or its _FILE
// form, else SourceDefault.
func (l *Layers) Source(name string) string {
	_, secret := spec(name)
	flag := settingFlag(name)
	if l.flags.Changed(flag) || secret && l.flags.Changed(flag+"-file") {
		return SourceFlag
	}
	if _, ok := l.env(name); ok {
		return SourceEnvironment
	}
	if _, ok := l.env(name + "_FILE"); ok {
		return SourceEnvironment
	}
	if _, ok := l.file[settingKey(name)]; ok {
		return SourceFile
	}
	if _, ok := l.file[settingKey(name)+"_file"]; ok {
		return SourceFile
	}
	return SourceDefault
}

// Lookup is the Lookup LoadConfig reads. A Settings row, with its _FILE form,
// comes from the one layer Source names, so a flag's value is never joined by
// an environment _FILE and made a conflict. Every other name is the
// environment's.
func (l *Layers) Lookup() Lookup {
	return func(key string) (string, bool) {
		name, form := key, ""
		if found, _ := spec(key); !found {
			base, ok := strings.CutSuffix(key, "_FILE")
			if found, _ := spec(base); !ok || !found {
				return l.env(key)
			}
			name, form = base, "_FILE"
		}
		switch l.Source(name) {
		case SourceFlag:
			flag := settingFlag(name)
			if form != "" {
				flag += "-file"
			}
			if l.flags.Changed(flag) {
				return l.flags.Lookup(flag).Value.String(), true
			}
		case SourceEnvironment:
			return l.env(key)
		case SourceFile:
			v, ok := l.file[settingKey(name)+strings.ToLower(form)]
			return v, ok
		}
		return "", false
	}
}

var settingName = regexp.MustCompile(`GOLDIE_[A-Z0-9_]+`)

// Annotate adds, to each error of err that names a setting, where the value of
// that setting came from: not a default, which is no one's choice to blame.
// The notes name the layer and never a value.
func (l *Layers) Annotate(err error) error {
	if err == nil {
		return nil
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		parts := multi.Unwrap()
		out := make([]error, len(parts))
		for i, e := range parts {
			out[i] = l.Annotate(e)
		}
		return errors.Join(out...)
	}
	var notes []string
	seen := map[string]bool{}
	for _, tok := range settingName.FindAllString(err.Error(), -1) {
		name := tok
		if found, _ := spec(name); !found {
			name = strings.TrimSuffix(tok, "_FILE")
		}
		if found, _ := spec(name); !found || seen[name] {
			continue
		}
		seen[name] = true
		switch l.Source(name) {
		case SourceFlag:
			notes = append(notes, fmt.Sprintf("%s given as the flag --%s", name, settingFlag(name)))
		case SourceEnvironment:
			notes = append(notes, name+" read from the environment")
		case SourceFile:
			notes = append(notes, settingKey(name)+" read from the config file")
		}
	}
	if len(notes) == 0 {
		return err
	}
	return fmt.Errorf("%s (%s)", err.Error(), strings.Join(notes, "; "))
}
