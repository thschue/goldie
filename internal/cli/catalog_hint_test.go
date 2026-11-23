package cli_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fix round for #409: show of an unknown skill or agent names the list
// command, and an empty list names a next step.

func catalogServer(t *testing.T, hint, listBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/skills" || r.URL.Path == "/api/v1/agents" {
			_, _ = w.Write([]byte(listBody))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such item","details":{"hint":"` + hint + `"}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCLIShowOfAnUnknownSkillOrAgentNamesTheListCommand(t *testing.T) {
	cases := []struct{ kind, plain, want string }{
		{"skills", "List the skills, then use a name from the list.", "List the skills with: goldie skills list, then use a name from its results."},
		{"agents", "List the agents, then use a name from the list.", "List the agents with: goldie agents list, then use a name from its results."},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			srv := catalogServer(t, c.plain, "")
			_, err := lookupRun(t, c.kind, "show", "nope", "--addr", srv.URL)
			if err == nil || !strings.Contains(err.Error(), "Hint: "+c.want) {
				t.Errorf("error = %v, want the hint %q", err, c.want)
			}
			other := catalogServer(t, "Some other hint.", "")
			_, err = lookupRun(t, c.kind, "show", "nope", "--addr", other.URL)
			if err == nil || !strings.Contains(err.Error(), "Hint: Some other hint.") {
				t.Errorf("error = %v, want another server hint unchanged", err)
			}
		})
	}
}

func TestCLIEmptyListNamesANextStep(t *testing.T) {
	for _, kind := range []string{"skills", "agents"} {
		t.Run(kind, func(t *testing.T) {
			srv := catalogServer(t, "", `{"`+kind+`":[]}`)
			out, err := lookupRun(t, kind, "list", "--addr", srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "goldie status") {
				t.Errorf("empty %s list = %q, want a next step naming goldie status", kind, out)
			}
		})
	}
}
