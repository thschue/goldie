package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance tests for #336: goldie init words a non-goldie answer from the
// projects endpoint as one fixed line, shows nothing of the page, the address
// or the token, and still writes the config; and the README's init paragraph
// says "not checked" also covers a server whose answer could not be read.

const initNotGoldieLine = "Registered: not checked (the server's answer isn't a goldie API answer; check the address)\n"

func TestCLIInitAgainstANonGoldieAnswerSaysNotChecked(t *testing.T) {
	cases := map[string]*httptest.Server{
		"html":         answerServer(t, 200, map[string]string{"Content-Type": "text/html"}, "<html>leak-body</html>"),
		"plain text":   answerServer(t, 200, map[string]string{"Content-Type": "text/plain"}, "leak-body"),
		"json no type": answerServer(t, 200, nil, `{"projects":["github.com/acme/widgets"],"leak":"leak-body"}`),
	}
	for name, srv := range cases {
		t.Run(name, func(t *testing.T) {
			root := gitRepo(t, widgetsOrigin)
			out, err := runInit(t, root, nil, "--addr", srv.URL, "--token", cliToken)
			if err != nil {
				t.Fatalf("init must exit 0: %v\n%s", err, out)
			}
			want := "Project: github.com/acme/widgets (from git remote origin)\n" +
				"Wrote: <root>/.goldie/config.yaml\n" + initNotGoldieLine
			if got := atRoot(t, out, root); got != want {
				t.Errorf("output:\n%s\nwant:\n%s", got, want)
			}
			if got := fileContent(t, filepath.Join(root, ".goldie", "config.yaml")); got != widgetsConfig {
				t.Errorf("config.yaml = %q, want it written", got)
			}
			for _, leak := range []string{"leak-body", "<html>", mustHost(t, srv.URL), cliToken} {
				if strings.Contains(out, leak) {
					t.Errorf("output leaks %q: %q", leak, out)
				}
			}
			if strings.Contains(out, "Hint:") || strings.Contains(out, "Registered: yes") || strings.Contains(out, "Registered: no\n") {
				t.Errorf("output claims a registration answer: %q", out)
			}
		})
	}
}

func TestReadmeInitParagraphSaysNotCheckedCoversAnUnreadableAnswer(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var para string
	for _, p := range strings.Split(string(b), "\n\n") {
		if strings.HasPrefix(p, "`goldie init") {
			para = p
		}
	}
	if para == "" {
		t.Fatal("README.md has no `goldie init` paragraph")
	}
	if !strings.Contains(para, "not checked") {
		t.Fatalf("init paragraph no longer mentions \"not checked\": %s", para)
	}
	flat := strings.Join(strings.Fields(para), " ")
	if !strings.Contains(flat, "answer couldn't be read") && !strings.Contains(flat, "answer could not be read") {
		t.Errorf("init paragraph must say \"not checked\" also covers a server whose answer couldn't be read: %s", flat)
	}
}
