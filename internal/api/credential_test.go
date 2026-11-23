package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// A Credential never shows its secrets, however it is logged or printed.
func TestCredentialNeverShowsItsSecrets(t *testing.T) {
	const tok, ses = "bearer-secret-marker", "cookie-secret-marker"
	c := Credential{Token: tok, SessionName: "goldie_session", SessionVal: ses}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "cred", c)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "cred", c)
	out := buf.String() + fmt.Sprintf("%v %+v %#v %s", c, c, c, c)
	for _, secret := range []string{tok, ses} {
		if strings.Contains(out, secret) {
			t.Errorf("output carries %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("output %q does not say the credential is redacted", out)
	}
}
