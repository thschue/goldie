package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
)

func (s settings) checkPullUsage(pos []string) error {
	switch {
	case len(pos) != 0:
		return &UsageError{"pull", "pull takes no arguments"}
	case s.target != "claude":
		return &UsageError{"pull", "--target must be claude"}
	case s.dryRun && s.check:
		return &UsageError{"pull", "--dry-run and --check cannot be used together"}
	}
	return nil
}

// bundleAnswer is the 200 of GET /api/v1/bundle: the archive and its ETag.
type bundleAnswer struct {
	archive []byte
	digest  string
}

// etagDigest reads the digest from an ETag value, "" when it is not one.
func etagDigest(etag string) string {
	d := strings.Trim(strings.TrimPrefix(etag, "W/"), `"`)
	if digestRE.MatchString(d) {
		return d
	}
	return ""
}

// pull is goldie pull: it fetches the bundle of the Project from the API and
// replaces the Goldie namespace at the repository root by it. The client holds
// no Resolution: the server resolves, the CLI checks the archive and writes it.
// With the stamp of the last pull it asks the server only whether the set
// changed (If-None-Match), so an unchanged set costs no archive and no write.
func (c *commander) pull(ctx context.Context) error {
	dir, err := os.Getwd()
	if err != nil {
		return errors.New("cannot read the working directory")
	}
	rootDir, ok := project.RepositoryRoot(dir)
	if !ok {
		return errors.New("not inside a git repository: goldie pull works at the repository root\nRun it in your repository, or create one first: git init")
	}
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return errors.New("cannot open the repository root")
	}
	defer func() { _ = root.Close() }()

	stamp := readStamp(root)
	if c.s.check && stamp == "" {
		return errors.New("the Goldie namespace was never pulled; run: goldie pull")
	}
	params := &contract.GetBundleParams{Target: c.s.target, Project: projectParam(proj)}
	if stamp != "" {
		inm := `"` + stamp + `"`
		params.IfNoneMatch = &inm
	}
	raw, err := cl.GetBundle(ctx, params)
	if err == nil && raw.StatusCode == http.StatusNotModified {
		_ = raw.Body.Close()
		return c.say("The Goldie namespace is already up to date.")
	}
	res, err := contract.Answer(raw, err, contract.ParseGetBundleResponse, func(r *contract.GetBundleResponse) *bundleAnswer {
		if !strings.HasPrefix(r.HTTPResponse.Header.Get("Content-Type"), "application/gzip") {
			return nil
		}
		return &bundleAnswer{archive: r.Body, digest: etagDigest(r.HTTPResponse.Header.Get("ETag"))}
	})
	if err != nil {
		return wordError(err, c.local)
	}
	if res.digest == "" {
		return errors.New("the server's bundle has no usable ETag; nothing was written")
	}
	if res.digest == stamp {
		return c.say("The Goldie namespace is already up to date.")
	}
	if c.s.check {
		return errors.New("the Goldie namespace is stale; run: goldie pull")
	}
	files, err := extractBundle(res.archive)
	if err != nil {
		return err
	}
	if c.s.dryRun {
		return c.dryRun(root, files)
	}
	if err := applyPull(root, files, res.digest); err != nil {
		return err
	}
	if err := c.say(fmt.Sprintf("Pulled %d files into the Goldie namespace (.claude/skills/goldie-*, .claude/agents/goldie-*).", len(files))); err != nil {
		return err
	}
	for _, l := range loopInvocations(files) {
		if err := c.say(l); err != nil {
			return err
		}
	}
	return nil
}

// dryRun lists what a pull would write and remove, and writes nothing.
func (c *commander) dryRun(root *os.Root, files []pulledFile) error {
	old, err := checkedNamespaceEntries(root)
	if err != nil {
		return err
	}
	fresh := map[string]bool{}
	for _, f := range files {
		fresh[topEntry(f.path)] = true
	}
	for _, o := range old {
		if !fresh[o] {
			if err := c.say("would remove " + contract.CleanLine(o)); err != nil {
				return err
			}
		}
	}
	for _, f := range files {
		if err := c.say("would write " + f.path); err != nil {
			return err
		}
	}
	return nil
}

func (c *commander) say(line string) error {
	_, err := fmt.Fprintln(c.out, line)
	return err
}
