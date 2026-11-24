package platform

import "testing"

// FuzzLayersConfigFile feeds hostile YAML to the config file layer: it must
// never panic, and every Lookup of a setting over what it accepted must answer.
func FuzzLayersConfigFile(f *testing.F) {
	for _, seed := range []string{
		"",
		"poll_interval: 45s\n",
		"sources:\n  - name: a\n    type: dir\n    dir: .\n",
		"github_orgs: [a, b]\n",
		"git_url: https://u:p@h/r.git\n",
		"api_token: x\n",
		"a: &x [*x]\n",
		"? [a]\n: b\n",
		"poll_interval: {a: b}\n",
		"\x00: \x00\n",
		"config: other.yaml\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, file string) {
		l, err := layersFor(t, nil, nil, file)
		if err != nil || l == nil {
			return
		}
		lookup := l.Lookup()
		for _, s := range Settings {
			lookup(s.Name)
			lookup(s.Name + "_FILE")
			l.Source(s.Name)
		}
	})
}
