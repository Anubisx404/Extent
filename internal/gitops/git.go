package gitops

import (
	"errors"
	"os/exec"
	"strings"
)

func EnsureBranch(root, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return errors.New("branch name cannot be empty")
	}
	if err := run(root, "git", "rev-parse", "--is-inside-work-tree"); err != nil {
		return err
	}
	if dirty, err := HasUncommittedChanges(root); err != nil {
		return err
	} else if dirty {
		return errors.New("working tree has uncommitted changes; commit, stash, or use a clean repo before branch creation")
	}
	if branchExists(root, branch) {
		return run(root, "git", "checkout", branch)
	}
	return run(root, "git", "checkout", "-b", branch)
}

func HasUncommittedChanges(root string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, errors.New(strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func branchExists(root, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+branch)
	cmd.Dir = root
	return cmd.Run() == nil
}

func run(root, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}
