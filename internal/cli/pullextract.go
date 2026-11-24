package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"

	"github.com/thschue/goldie/internal/contract"
)

// Limits on what one pull accepts, so a hostile or broken server cannot make
// the CLI buffer or write without bound. The compressed body is already capped
// at contract.MaxResponseBytes.
const (
	maxPullFiles = 10000
	maxPullBytes = 64 << 20
)

// errBundleTooLarge is the answer to a bundle over what the client reads. It
// names the limit and a next step, in the wording of the server's 422.
var errBundleTooLarge = fmt.Errorf("the bundle is larger than %d MiB and nothing was written: shrink or remove a skill with large files (binary assets) in a Source", contract.MaxResponseBytes>>20)

// goldiePrefix starts every name in the Goldie namespace: the skill
// directories .claude/skills/goldie-* and the agent files
// .claude/agents/goldie-*.md. A pull replaces these and touches nothing else.
const goldiePrefix = "goldie-"

// pulledFile is one regular file of a checked archive.
type pulledFile struct {
	path string // clean slash path from the repository root, inside the namespace
	exec bool
	data []byte
}

// errBundle is the answer to an archive that is not a bundle. It names nothing
// of the archive.
var errBundle = errors.New("the server's bundle is not a valid archive; nothing was written")

// extractBundle reads the whole gzip-compressed tar in body into memory and
// checks every entry before anything is written, so a refused archive leaves
// the project as it was. Only regular files are accepted, each at a clean
// relative path inside the Goldie namespace: no absolute path, no "..", no
// link, device or fifo, no duplicate, and no path that is both a file and a
// directory. A file is executable when its mode has any executable bit.
func extractBundle(body []byte) ([]pulledFile, error) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, errBundle
	}
	tr := tar.NewReader(gz)
	var files []pulledFile
	taken := claimed{files: map[string]bool{}, dirs: map[string]bool{}}
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errBundle
		}
		if h.Typeflag != tar.TypeReg {
			return nil, refusedEntry(h.Name, "is not a regular file")
		}
		if !inNamespace(h.Name) {
			return nil, refusedEntry(h.Name, "is not a path of the Goldie namespace")
		}
		if len(files) >= maxPullFiles || h.Size > maxPullBytes-total {
			return nil, errors.New("the server's bundle is larger than goldie pull accepts; nothing was written")
		}
		if err := taken.claim(h.Name); err != nil {
			return nil, refusedEntry(h.Name, err.Error())
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, errBundle
		}
		total += int64(len(data))
		files = append(files, pulledFile{path: h.Name, exec: h.Mode&0o111 != 0, data: data})
	}
	// Read to the end of the stream so a truncated or corrupt archive fails
	// its checksum.
	if _, err := io.Copy(io.Discard, io.LimitReader(gz, maxPullBytes)); err != nil {
		return nil, errBundle
	}
	return files, nil
}

func refusedEntry(name, why string) error {
	return fmt.Errorf("the server's bundle has an entry %q that %s; nothing was written", contract.CleanLine(name), why)
}

// claimed is the set of paths an archive has taken so far.
type claimed struct{ files, dirs map[string]bool }

// claim records p as a file and the directories above it, and refuses a path
// that is already a file or a directory, or that sits inside a file.
func (c *claimed) claim(p string) error {
	if c.files[p] || c.dirs[p] {
		return errors.New("appears twice or is both a file and a directory")
	}
	c.files[p] = true
	for d := path.Dir(p); d != "."; d = path.Dir(d) {
		if c.files[d] {
			return errors.New("is inside a file")
		}
		c.dirs[d] = true
	}
	return nil
}

// inNamespace reports whether name is a clean relative slash path of the
// Goldie namespace: a file inside .claude/skills/goldie-<name>/, or the file
// .claude/agents/goldie-<name>.md. Anything absolute, with a backslash or a
// character that is not printable, or that path.Clean would change (so any
// "." or ".." element, empty element or trailing slash) is outside it.
func inNamespace(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, '\\') || path.Clean(name) != name {
		return false
	}
	for _, r := range name {
		if !unicode.IsGraphic(r) {
			return false
		}
	}
	parts := strings.Split(name, "/")
	if len(parts) < 3 || parts[0] != ".claude" || !strings.HasPrefix(parts[2], goldiePrefix) || parts[2] == goldiePrefix {
		return false
	}
	switch parts[1] {
	case "skills":
		return len(parts) >= 4
	case "agents":
		return len(parts) == 3 && strings.HasSuffix(parts[2], ".md") && parts[2] != goldiePrefix+".md"
	}
	return false
}
