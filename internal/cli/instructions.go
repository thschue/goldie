package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
)

// instructionFiles maps --target to the file at the repository root.
var instructionFiles = map[string]string{"claude": "CLAUDE.md", "agents": "AGENTS.md"}

func (s settings) checkInstructionsUsage(pos []string) error {
	if len(pos) != 1 || (pos[0] != "apply" && pos[0] != "check") {
		return &UsageError{"instructions", "instructions takes apply or check"}
	}
	if _, ok := instructionFiles[s.target]; !ok {
		return &UsageError{"instructions", "--target must be claude or agents"}
	}
	if s.force && pos[0] == "check" {
		return &UsageError{"instructions", "--force applies to apply only"}
	}
	return nil
}

// applyInstructions is goldie instructions apply for target, without --force,
// as init runs it.
func (c *commander) applyInstructions(ctx context.Context, target string) error {
	c.s.target, c.s.force = target, false
	return c.instructions(ctx, "apply")
}

// instructions is goldie instructions apply and check. It reads the block from
// the API, then reads the target file at the repository root through an
// os.Root, so no path above the root is touched. Both commands write nothing
// on a refusal; check never writes.
//
// Lost-update window: apply reads the file, then renames a temporary file over
// it. An edit made by another process between the read and the rename is overwritten.
// There is no lock; the window is as short as one local write.
func (c *commander) instructions(ctx context.Context, action string) error {
	name := instructionFiles[c.s.target]
	dir, err := os.Getwd()
	if err != nil {
		return errors.New("cannot read the working directory")
	}
	rootDir, ok := project.RepositoryRoot(dir)
	if !ok {
		return errors.New("not inside a git repository: goldie instructions works on " + name + " at the repository root\nRun it in your repository, or create one first: git init")
	}
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	raw, err := cl.GetInstructions(ctx, &contract.GetInstructionsParams{Target: c.s.target, Project: projectParam(proj)})
	res, err := contract.Answer(raw, err, contract.ParseGetInstructionsResponse,
		func(r *contract.GetInstructionsResponse) *contract.InstructionBlock { return r.JSON200 })
	if err != nil {
		return wordError(err, c.local)
	}
	// The block is untrusted Source content: strip hidden and bidi characters.
	block := []byte(contract.CleanText(res.Block))
	if err := checkBlockContent(block); err != nil {
		return err
	}

	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return errors.New("cannot open the repository root")
	}
	defer func() { _ = root.Close() }()
	file, mode, err := readRootFile(root, name)
	if errors.Is(err, errNotRegular) { // a CLAUDE.md linked to AGENTS.md is managed through the target
		return fmt.Errorf("%w. Manage the file it points to, for example with --target agents", err)
	}
	if err != nil {
		return err
	}
	// check replaces with force so an edited block reports its status.
	out, status, err := ReplaceBlock(file, block, action == "check" || c.s.force)
	if err != nil {
		// The messages say what is wrong; the caller names the file and, for an
		// edited block, the next command.
		if errors.Is(err, ErrBlockEdited) {
			return editedError(fmt.Sprintf("%s: the goldie block was edited by hand. To replace it and discard those edits, run: goldie instructions apply --target %s --force", name, c.s.target))
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	if action == "check" {
		if status != BlockCurrent {
			fix := "goldie instructions apply --target " + c.s.target
			if status == BlockEdited {
				fix += " --force"
			}
			return fmt.Errorf("the goldie block in %s is %s; run: %s", name, status, fix)
		}
		_, err = fmt.Fprintf(c.out, "%s is up to date.\n", name)
		return err
	}
	if status == BlockCurrent {
		_, err = fmt.Fprintf(c.out, "%s is already up to date.\n", name)
		return err
	}
	if err := writeRootFile(root, name, out, mode); err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "Wrote the goldie block to %s (was %s).\n", name, status)
	return err
}

// maxEditedFile is the most readRootFile reads: a file goldie edits in place
// (CLAUDE.md, AGENTS.md, .claude/settings.json) is text a person keeps, so a
// larger one is refused, not buffered.
const maxEditedFile = 4 << 20

// errNotRegular is a file that is a symlink, a directory or another irregular
// file. The caller adds the next step.
var errNotRegular = errors.New("is not a regular file (a symlink?); goldie never follows or replaces it")

// errTooLarge is a file over maxEditedFile.
var errTooLarge = fmt.Errorf("is larger than %d MiB, so goldie does not edit it", maxEditedFile>>20)

// readRootFile reads name under root. A missing file is empty content. A
// symlink, a directory or any other irregular file is refused with errNotRegular,
// and a file over maxEditedFile with errTooLarge: both wrapped after the name.
func readRootFile(root *os.Root, name string) ([]byte, os.FileMode, error) {
	fi, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, 0o644, nil
	case err != nil:
		return nil, 0, fmt.Errorf("cannot read %s", name)
	case !fi.Mode().IsRegular():
		return nil, 0, fmt.Errorf("%s %w", name, errNotRegular)
	case fi.Size() > maxEditedFile:
		return nil, 0, fmt.Errorf("%s %w", name, errTooLarge)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read %s", name)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxEditedFile+1))
	switch {
	case err != nil:
		return nil, 0, fmt.Errorf("cannot read %s", name)
	case len(b) > maxEditedFile:
		return nil, 0, fmt.Errorf("%s %w", name, errTooLarge)
	}
	return b, fi.Mode().Perm(), nil
}

// tempName is the temporary file next to name: a dot, at most 100 bytes of
// name, 16 random hex digits and ".tmp", so it stays within NAME_MAX (255)
// whatever name is.
func tempName(name string) string {
	if len(name) > 100 {
		name = name[:100]
	}
	var r [8]byte
	_, _ = rand.Read(r[:])
	return "." + name + "." + hex.EncodeToString(r[:]) + ".tmp"
}

// writeRootFile replaces name under root by data: it writes a
// temporary file in the same directory, syncs it and renames it over name.
func writeRootFile(root *os.Root, name string, data []byte, mode os.FileMode) (err error) {
	tmp := tempName(name)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("cannot write %s", name)
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("cannot write %s", name)
	}
	if err := root.Rename(tmp, name); err != nil {
		return fmt.Errorf("cannot write %s", name)
	}
	return nil
}

var (
	// fenceRE matches a fence line as CommonMark does: at most three spaces of
	// indent, a run of three or more backticks or tildes, then the rest.
	fenceRE   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	htmlRE    = regexp.MustCompile(`^\s*<[A-Za-z/!?]`)
	linkDefRE = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:`)
)

// checkBlockContent refuses block text that could change how the user's text
// after the block renders: an unclosed code fence, a raw HTML block or
// comment, or a link reference definition (which applies to the whole file).
// Markers are refused by ReplaceBlock. The block is untrusted Source content.
func checkBlockContent(block []byte) error {
	var fence string // the open fence's run of markers, "" outside one
	for _, line := range strings.Split(string(block), "\n") {
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			run, rest := m[1], m[2]
			switch {
			case fence == "":
				// A backtick fence's info string holds no backtick.
				if run[0] != '`' || !strings.Contains(rest, "`") {
					fence = run
					continue
				}
			case run[0] == fence[0] && len(run) >= len(fence) && strings.TrimSpace(rest) == "":
				fence = ""
				continue
			}
		}
		switch {
		case fence != "":
		case htmlRE.MatchString(line):
			return errBlockRender("raw HTML")
		case linkDefRE.MatchString(line):
			return errBlockRender("a link reference definition")
		}
	}
	if fence != "" {
		return errBlockRender("a code fence that is never closed")
	}
	return nil
}

func errBlockRender(what string) error {
	return fmt.Errorf("the block the server sent holds %s, which could change how the rest of the file renders; nothing was written. Report it to the platform team", what)
}
