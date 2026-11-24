package app

import (
	"bytes"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #697 (goldie pull on a bundle over the size the client
// accepts), at the CLI boundary against the real public handler and, for a
// server that sends more than the client reads, against a stub. Pattern:
// pull_test.go (pullServer, copyContent, pullCLI, gitCheckout, snapshot).
//
// The contract they fix: a bundle larger than contract.MaxResponseBytes
// (16 MiB) makes goldie pull fail with an error that names that limit in MiB
// and tells the user what to do (shrink a skill), never the generic "the
// server's answer is larger than ..." wording or a response error, and it
// writes nothing. A bundle under the limit pulls as before.

// noise returns n bytes gzip cannot shrink, so the archive is about n bytes.
func noise(n int) []byte {
	var seed [32]byte
	r := rand.NewChaCha8(seed)
	b := make([]byte, n)
	_, _ = r.Read(b)
	return b
}

// assetChunk is the size of one asset file; a skill file is capped at 1 MiB.
const assetChunk = 512 << 10

// addBigSkill adds a skill with binary assets of n incompressible bytes in all,
// in files of assetChunk, to the global set of content. assetFile names the
// i-th one.
func addBigSkill(t *testing.T, content string, n int) {
	t.Helper()
	dir := filepath.Join(content, "global", "skills", "bigassets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Big assets\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all := noise(n)
	for i := 0; i*assetChunk < n; i++ {
		part := all[i*assetChunk : min(n, (i+1)*assetChunk)]
		if err := os.WriteFile(filepath.Join(dir, assetFile(i)), part, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assetFile(i int) string { return "asset-" + strconv.Itoa(i) + ".bin" }

// wantSizeLimitError checks the error of a refused oversized pull: it names
// the limit and a next step, and is none of the generic wordings.
func wantSizeLimitError(t *testing.T, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("an oversized bundle was pulled:\n%s", out)
	}
	if contract.MaxResponseBytes != 16<<20 {
		t.Fatalf("the limit changed to %d bytes; update this test with the spec", contract.MaxResponseBytes)
	}
	msg := err.Error()
	if !strings.Contains(msg, "16 MiB") {
		t.Errorf("the error does not name the 16 MiB limit: %q", msg)
	}
	low := strings.ToLower(msg)
	if !strings.Contains(low, "bundle") {
		t.Errorf("the error does not say it is the bundle that is too large: %q", msg)
	}
	if !strings.Contains(low, "skill") {
		t.Errorf("the error gives no next step (shrink or remove a skill): %q", msg)
	}
	for _, generic := range []string{"the server's answer", "cannot reach the server", "isn't a goldie api answer", "the server answered"} {
		if strings.Contains(low, generic) {
			t.Errorf("the error is a generic response error (%q): %q", generic, msg)
		}
	}
}

// Criterion: a bundle larger than the client accepts fails with an error that
// names the size limit and a next step, never a generic response error.
func TestPullOfABundleOverTheLimitNamesTheLimitAndANextStep(t *testing.T) {
	content := copyContent(t)
	addBigSkill(t, content, contract.MaxResponseBytes+(1<<20))
	repo := gitCheckout(t, userFiles())
	before := snapshot(t, repo)

	out, err := pullCLI(t, pullServer(t, content).URL, repo)
	wantSizeLimitError(t, out, err)
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("a refused pull changed the project: %v then %v", before, after)
	}
}

// Criterion: the same holds when the server does not check and sends more than
// the client reads (the limit is the client's, not only the server's), and
// dry-run refuses it the same way.
func TestPullOfABundleTheServerDoesNotCapNamesTheLimit(t *testing.T) {
	big := bytes.Repeat([]byte("x"), contract.MaxResponseBytes+1024)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("ETag", `"oversized-stub"`)
		_, _ = w.Write(big)
	}))
	t.Cleanup(stub.Close)

	for _, args := range [][]string{nil, {"--dry-run"}} {
		repo := gitCheckout(t, userFiles())
		before := snapshot(t, repo)
		out, err := pullCLI(t, stub.URL, repo, args...)
		wantSizeLimitError(t, out, err)
		if after := snapshot(t, repo); !mapsEqual(before, after) {
			t.Errorf("a refused pull %v changed the project: %v then %v", args, before, after)
		}
	}
}

// Criterion: bundles under the limit pull unchanged. A bundle of about 15 MiB
// is written whole, byte for byte.
func TestPullOfABundleUnderTheLimitPullsUnchanged(t *testing.T) {
	content := copyContent(t)
	const n = 15 << 20
	addBigSkill(t, content, n)
	repo := gitCheckout(t, nil)

	if out, err := pullCLI(t, pullServer(t, content).URL, repo); err != nil {
		t.Fatalf("a bundle under the limit was refused: %v\n%s", err, out)
	}
	all := noise(n)
	for i := 0; i*assetChunk < n; i++ {
		got, err := os.ReadFile(filepath.Join(repo, ".claude", "skills", "goldie-bigassets", assetFile(i)))
		if err != nil {
			t.Fatalf("asset %d was not written: %v", i, err)
		}
		if !bytes.Equal(got, all[i*assetChunk:min(n, (i+1)*assetChunk)]) {
			t.Fatalf("asset %d differs from the source (%d bytes)", i, len(got))
		}
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(pDeploySkill))); err != nil {
		t.Errorf("the rest of the set was not written: %v", err)
	}
}
