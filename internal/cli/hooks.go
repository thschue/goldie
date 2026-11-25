package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/thschue/goldie/internal/contract/project"
)

// HooksInstall is goldie hooks install: it puts goldie's SessionStart hook in
// .claude/settings.json at the repository root, creating the file if needed.
// pos are the words after hooks; only install is a verb. It never touches
// settings.local.json, writes nothing on a refusal, and keeps the mode of an
// existing file. It needs no server and no token.
//
// Lost-update window, as for goldie instructions apply: an edit made to the
// file between the read and the rename is overwritten; there is no lock.
func HooksInstall(pos []string, out io.Writer) error {
	if len(pos) != 1 || pos[0] != "install" {
		return &UsageError{"hooks", "hooks takes install"}
	}
	dir, err := os.Getwd()
	if err != nil {
		return errors.New("cannot read the working directory")
	}
	rootDir, ok := project.RepositoryRoot(dir)
	if !ok {
		return hookRefusal{"not inside a git repository: goldie hooks install works on " + settingsFile + " at the repository root",
			"Run it in your repository, or create one first: git init"}
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return errors.New("cannot open the repository root")
	}
	defer func() { _ = root.Close() }()

	// .claude is checked first: a root follows a link that stays inside it.
	exists, err := plainDir(root, ".claude")
	if err != nil {
		return hookRefusal{err.Error(), "Replace it with a real directory, then run goldie hooks install again."}
	}
	file, mode, err := readRootFile(root, settingsFile)
	if err != nil {
		return hookRefusal{err.Error(), "Edit the file by hand, or replace it with a regular file, then run goldie hooks install again."}
	}
	edited, status, err := InstallSessionStartHook(file)
	if err != nil {
		return err
	}
	if status == HookCurrent {
		_, err = fmt.Fprintf(out, "The goldie SessionStart hook in %s is already up to date.\n", settingsFile)
		return err
	}
	if !exists {
		if err := root.Mkdir(".claude", 0o755); err != nil {
			return errors.New("cannot create .claude")
		}
	}
	claude, err := root.OpenRoot(".claude")
	if err != nil {
		return errors.New("cannot open .claude")
	}
	defer func() { _ = claude.Close() }()
	if err := writeRootFile(claude, "settings.json", edited, mode); err != nil {
		return err
	}
	verb := "Added the goldie SessionStart hook to"
	if status == HookUpdated {
		verb = "Updated the goldie SessionStart hook in"
	}
	_, err = fmt.Fprintf(out, "%s %s.\n", verb, settingsFile)
	return err
}
