package app

import "testing"

// --if-pulled outside a git repository has no stamp to find: it succeeds,
// sends no request and prints nothing; without the flag it is still an error.
// Pattern: pull_test.go (countingPullServer, pullCLI) and outsideRepository.
func TestPullIfPulledOutsideARepositoryDoesNothing(t *testing.T) {
	srv, rec := countingPullServer(t, absContent(t))
	dir := outsideRepository(t)
	out, err := pullCLI(t, srv.URL, dir, "--project", pullProject, "--if-pulled", "--quiet")
	if err != nil || out != "" {
		t.Errorf("--if-pulled outside a repository: err %v, stdout %q", err, out)
	}
	if bundleCalls(rec) != 0 {
		t.Errorf("--if-pulled outside a repository sent a request")
	}
	if _, err := pullCLI(t, srv.URL, dir, "--project", pullProject); err == nil {
		t.Errorf("a plain pull outside a repository succeeded")
	}
}
