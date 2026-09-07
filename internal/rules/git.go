package rules

import (
	"os"
	"path/filepath"
	"strings"
)

// protectedBranches are the branches worth a prompt to act on directly.
var protectedBranches = map[string]bool{"main": true, "master": true}

// repoRoot walks up from dir looking for ".git" as either a directory or a
// file. A worktree's ".git" is a file, and resolving to the worktree root is
// the correct answer for it.
//
// No git subprocess is involved, just a few filesystem stats.
func repoRoot(dir string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}

	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

// onProtectedBranch reports whether HEAD in the repository containing dir is
// on main or master.
//
// It fails toward the prompt: a cwd that cannot be resolved, a missing
// repository or an unreadable HEAD all count as protected, because there is
// then no evidence that the branch is safe.
func onProtectedBranch(dir string) bool {
	branch, known := HeadBranch(dir)
	if !known {
		return true
	}

	return protectedBranches[branch]
}

// RepoRoot returns the root of the git repository containing dir, and whether
// one was found. It is exported so `bouncer explain` can show the boundary the
// rm rule judged a path against.
func RepoRoot(dir string) (string, bool) {
	return repoRoot(dir)
}

// HeadBranch returns the branch HEAD points at in the repository containing
// dir.
//
// The second return value is false when there is no evidence of a branch at
// all: no repository, or an unreadable HEAD. A detached HEAD reports an empty
// branch name and true, because that is known not to be a protected branch.
func HeadBranch(dir string) (string, bool) {
	root, found := repoRoot(dir)
	if !found {
		return "", false
	}

	gitDir, err := resolveGitDir(root)
	if err != nil {
		return "", false
	}

	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", false
	}

	ref, isRef := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	if !isRef {
		return "", true
	}

	return ref, true
}

// resolveGitDir returns the real git directory for a repository root,
// following the "gitdir:" pointer that a worktree's .git file holds.
func resolveGitDir(root string) (string, error) {
	path := filepath.Join(root, ".git")

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if info.IsDir() {
		return path, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	target, isPointer := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !isPointer {
		return "", os.ErrInvalid
	}

	if filepath.IsAbs(target) {
		return target, nil
	}

	return filepath.Join(root, target), nil
}
