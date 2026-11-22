package contract

import (
	"os"
	"strings"
	"testing"
)

func TestReferenceDocumentsEveryOperationAndSchema(t *testing.T) {
	out, err := Reference(SpecYAML())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### `GET /openapi.json`", "### `GET /openapi.yaml`", "Operation ID: `getOpenAPIJSON`",
		"| `default` | An error, in the error envelope. |", "### `ErrorEnvelope`", "| `details` | `ErrorDetails` | yes |", "### `ErrorDetails`",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("reference lacks %q:\n%s", want, out)
		}
	}
}

func TestReferenceIsDeterministicAndEscapesSpecText(t *testing.T) {
	spec := []byte(`openapi: 3.1.0
info: {title: T, version: "1"}
paths:
  /x:
    get:
      operationId: x
      responses:
        "200": {description: "a | b <script>alert(1)</script>"}
`)
	a, err := Reference(spec)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Reference(spec)
	if string(a) != string(b) {
		t.Fatal("two renders differ")
	}
	if strings.Contains(string(a), "<script>") || !strings.Contains(string(a), `a \| b &lt;script&gt;`) {
		t.Fatalf("spec text not escaped:\n%s", a)
	}
}

func TestReferenceRejectsADocumentWithoutPaths(t *testing.T) {
	if _, err := Reference([]byte("openapi: 3.1.0\n")); err == nil {
		t.Fatal("want an error")
	}
}

// The committed reference must equal a fresh render; `./run regen` rewrites it.
func TestCommittedReferenceMatchesTheSpec(t *testing.T) {
	want, err := Reference(SpecYAML())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/reference/api.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("docs/reference/api.md is out of date with openapi.yaml (run: ./run regen)")
	}
}
