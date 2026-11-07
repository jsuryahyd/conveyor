package core

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

// execCommand is swappable in tests.
var execCommand = exec.CommandContext

func git(path string, args ...string) (string, error) {
	cmd := execCommand(context.Background(), "git", append([]string{"-C", path}, args...)...)
	b, err := cmd.Output()
	return strings.TrimSpace(string(b)), err
}

// AddRepo validates that path is the top level of a git clone, detects its
// default branch (origin/HEAD, else the current branch) and registers it.
// name defaults to the basename of path.
func (s *Service) AddRepo(path, name string) (store.Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return store.Repo{}, fmt.Errorf("resolve path: %w", err)
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return store.Repo{}, fmt.Errorf("resolve path: %w", err)
	}
	top, err := git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return store.Repo{}, fmt.Errorf("%s is not a git repository: %w", abs, err)
	}
	if top, err = filepath.EvalSymlinks(top); err != nil || top != abs {
		return store.Repo{}, fmt.Errorf("%s is not the top level of a git repo (top level is %s)", abs, top)
	}
	branch := ""
	if ref, err := git(abs, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		branch = strings.TrimPrefix(ref, "refs/remotes/origin/")
	}
	if branch == "" {
		branch, _ = git(abs, "branch", "--show-current")
	}
	if branch == "" {
		return store.Repo{}, fmt.Errorf("cannot detect default branch of %s", abs)
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	r, err := store.AddRepo(s.DB, store.Repo{Name: name, Path: abs, DefaultBranch: branch})
	if err != nil {
		return r, err
	}
	_ = agy.AddTrustedWorkspaces(s.Home, abs)
	return r, nil
}
