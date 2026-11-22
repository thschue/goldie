package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
)

const (
	tokA = "token-a-0123456789abcdef0123456789abcdef"
	tokB = "token-b-0123456789abcdef0123456789abcdef"
)

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func status(t *testing.T, h http.Handler, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

var named = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	name, _ := api.TokenName(r.Context())
	_, _ = w.Write([]byte(name))
})

// An operation that needs the operator Role answers a reader's token with 403
// in the envelope, and an operator's with the handler.
func TestRequireOperatorRole(t *testing.T) {
	tokens, err := api.ParseTokens("r:reader:" + tokA + "\no:operator:sha256:" + digest(tokB))
	if err != nil {
		t.Fatal(err)
	}
	h := tokens.Require(api.RoleOperator, named)
	if code, body := status(t, h, tokA); code != http.StatusForbidden || !strings.Contains(body, `"forbidden"`) || strings.Contains(body, tokA) {
		t.Errorf("reader on an operator operation: %d %s, want 403 forbidden", code, body)
	}
	if code, body := status(t, h, tokB); code != http.StatusOK || body != "o" {
		t.Errorf("operator: %d %q, want 200 o", code, body)
	}
	if code, _ := status(t, h, ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}
}

// Blank lines, # comments and CRLF line ends are accepted; the line numbers in
// errors count them.
func TestParseTokensSkipsBlankAndCommentLines(t *testing.T) {
	tokens, err := api.ParseTokens("# CI\r\n\r\nci:reader:" + tokA + "\r\n\n# old:reader:" + tokB + "\n")
	if err != nil {
		t.Fatal(err)
	}
	h := tokens.Require(api.RoleReader, named)
	if code, body := status(t, h, tokA); code != http.StatusOK || body != "ci" {
		t.Errorf("token a: %d %q, want 200 ci", code, body)
	}
	if code, _ := status(t, h, tokB); code != http.StatusUnauthorized {
		t.Errorf("commented-out token: %d, want 401", code)
	}
	if _, err := api.ParseTokens("# x\n\nci:reader:short"); err == nil || !strings.HasPrefix(err.Error(), "line 3:") {
		t.Errorf("err = %v, want it to name line 3", err)
	}
}

// A set with no token lines, a reused name, a reused token (plaintext or
// hashed) and a name unsafe for logs fail without echoing the token.
func TestParseTokensRejectsAmbiguousSets(t *testing.T) {
	for name, lines := range map[string]string{
		"only comments":     "# nothing\n",
		"same name":         "ci:reader:" + tokA + "\nci:operator:" + tokB,
		"same token":        "a:reader:" + tokA + "\nb:operator:" + tokA,
		"same token hashed": "a:reader:" + tokA + "\nb:operator:sha256:" + digest(tokA),
		"upper-case digest": "a:reader:sha256:" + digest(tokA) + "\nb:reader:sha256:" + strings.ToUpper(digest(tokA)),
		"name with a space": "c i:reader:" + tokA,
		"name too long":     strings.Repeat("n", 65) + ":reader:" + tokA,
		"role in caps":      "ci:Reader:" + tokA,
	} {
		_, err := api.ParseTokens(lines)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), tokA) || strings.Contains(err.Error(), digest(tokA)) {
			t.Errorf("%s: error echoes a secret: %v", name, err)
		}
	}
}

// The zero and nil token sets reject every request.
func TestEmptyTokenSetRejectsEverything(t *testing.T) {
	for name, tokens := range map[string]*api.Tokens{"zero": {}, "nil": nil} {
		if code, _ := status(t, tokens.Require(api.RoleReader, named), tokA); code != http.StatusUnauthorized {
			t.Errorf("%s set: %d, want 401", name, code)
		}
	}
}
