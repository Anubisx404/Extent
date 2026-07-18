package gitops

import (
	"context"
	"errors"
	"fmt"
	"github.com/Anubisx404/Extent/internal/process"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) process.Result
}

func EnsureBranch(root, branch string) error {
	return EnsureBranchContext(context.Background(), process.Runner{Dir: root}, root, branch)
}
func EnsureBranchContext(ctx context.Context, r Runner, root, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return errors.New("branch name cannot be empty")
	}
	if e := run(ctx, r, root, "git", "rev-parse", "--is-inside-work-tree"); e != nil {
		return e
	}
	if e := run(ctx, r, root, "git", "check-ref-format", "--branch", branch); e != nil {
		return fmt.Errorf("invalid branch name: %w", e)
	}
	dirty, e := HasUncommittedChangesContext(ctx, r, root)
	if e != nil {
		return e
	}
	if dirty {
		return errors.New("working tree has uncommitted changes; commit, stash, or use a clean repo before branch creation")
	}
	exists, err := branchExists(ctx, r, root, branch)
	if err != nil {
		return err
	}
	if exists {
		return run(ctx, r, root, "git", "checkout", branch)
	}
	return run(ctx, r, root, "git", "checkout", "-b", branch)
}
func HasUncommittedChanges(root string) (bool, error) {
	return HasUncommittedChangesContext(context.Background(), process.Runner{Dir: root}, root)
}
func HasUncommittedChangesContext(ctx context.Context, r Runner, root string) (bool, error) {
	res := r.Run(ctx, "git", "status", "--porcelain")
	if res.Err != nil {
		return false, res.Err
	}
	return strings.TrimSpace(res.Stdout) != "", nil
}
func branchExists(ctx context.Context, r Runner, root, branch string) (bool, error) {
	result := r.Run(ctx, "git", "rev-parse", "--verify", "refs/heads/"+branch)
	if result.Err == nil {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git branch lookup failed: %w", result.Err)
}
func run(ctx context.Context, r Runner, root, n string, a ...string) error {
	res := r.Run(ctx, n, a...)
	if res.Err != nil {
		return fmt.Errorf("%s: %w", n, res.Err)
	}
	return nil
}
