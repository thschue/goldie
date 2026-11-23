package contract

import "testing"

func TestAnonymousOperationsComeFromTheSpec(t *testing.T) {
	got, err := AnonymousOperations()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"POST /api/v1/session", "DELETE /api/v1/session"} {
		if !got[op] {
			t.Errorf("%s is not anonymous", op)
		}
	}
	if got["GET /api/v1/session"] || got["GET /api/v1/search"] {
		t.Errorf("anonymous operations = %v, want no bearer-guarded one", got)
	}
}

func TestParseAnonymousFailsClosedOnOtherSecurity(t *testing.T) {
	for name, spec := range map[string]string{
		"a scheme":       "paths:\n  /a:\n    get:\n      security:\n        - bearer: []\n",
		"an empty alt":   "paths:\n  /a:\n    get:\n      security:\n        - {}\n",
		"a scalar":       "paths:\n  /a:\n    get:\n      security: none\n",
		"an alias to it": "paths:\n  /a:\n    get:\n      security: &s\n        - bearer: []\n  /b:\n    get:\n      security: *s\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := parseAnonymous([]byte(spec)); err == nil {
				t.Errorf("security other than [] was accepted, got %v", got)
			}
		})
	}
}

func TestParseAnonymousReadsAliasedOperations(t *testing.T) {
	spec := "paths:\n  /a:\n    get: &g\n      security: []\n    post: *g\n    put:\n      summary: guarded\n    parameters: []\n"
	got, err := parseAnonymous([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if !got["GET /a"] || !got["POST /a"] || got["PUT /a"] || len(got) != 2 {
		t.Errorf("got %v, want GET and POST /a only", got)
	}
}
