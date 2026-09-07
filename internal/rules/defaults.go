package rules

// Defaults returns the compiled rule list.
//
// The list is empirical: it was scored against 31 days of real session
// transcripts and costs roughly 58 prompts a month. Three rules that would
// have been the noisiest as flat command matches are narrowed by target
// instead, which cut 115 prompts a month to about six and made two of them
// more accurate rather than less. Do not widen it from intuition.
func Defaults() []Rule {
	rs := []Rule{
		// core: measured, in rough order of how often each fires.

		// the incident this tool exists for: "cd /elsewhere && rm -f FILE".
		// in-repo rm stays allowed.
		{Name: "rm-outside-repo", Type: TypeArgsOutsideRepo, Args: []string{"rm"}},

		// bouncer's own config is on this list so it cannot silently rewrite
		// its own rules.
		{Name: "config-paths", Type: TypePathGlob, Args: []string{
			"**/settings.json",
			"**/settings.local.json",
			"**/.claude/hooks/**",
			"**/.env*",
			"**/*strongbox-keyring",
			"~/.ssh/**",
			"~/.aws/**",
			"~/.kube/**",
			"~/.config/bouncer/**",
		}},

		// network egress.
		{Name: "network-fetch", Type: TypeCmd, Args: []string{"curl", "wget"}},

		// discards uncommitted work.
		{Name: "git-reset-hard", Type: TypeFlag, Args: []string{"git", "reset", "--hard"}},

		// the stash stack is shared across worktrees.
		{Name: "git-stash", Type: TypeSub, Args: []string{"git", "stash"}},

		// supply chain.
		{Name: "go-install", Type: TypeSub, Args: []string{"go", "install"}},
		{Name: "npm-install", Type: TypeSub, Args: []string{"npm", "install", "add"}},
		{Name: "yarn-install", Type: TypeSub, Args: []string{"yarn", "install", "add"}},
		{Name: "pnpm-install", Type: TypeSub, Args: []string{"pnpm", "install", "add"}},
		{Name: "brew-install", Type: TypeSub, Args: []string{"brew", "install"}},
		{Name: "go-mod", Type: TypeSub, Args: []string{"go", "mod"}},

		{Name: "file-permissions", Type: TypeCmd, Args: []string{"chmod", "chown"}},
		{Name: "git-branch-delete", Type: TypeFlag, Args: []string{"git", "branch", "-D"}},
		{Name: "git-config", Type: TypeSub, Args: []string{"git", "config"}},

		// git push, three independent conditions, either one enough to prompt.
		{Name: "git-push-protected-target", Type: TypeArgIn, Args: []string{"git", "push", "main", "master"}},
		// --force-with-lease is deliberately absent: it refuses if the remote
		// moved, and it is the standard PR-update workflow.
		{Name: "git-push-force", Type: TypeFlag, Args: []string{"git", "push", "--force", "-f"}},
		{Name: "git-push-on-protected-branch", Type: TypeOnProtectedBranch, Args: []string{"git", "push"}},

		// for rebase the current branch is what matters, not the target:
		// rebasing a feature branch onto main is normal daily work.
		{Name: "git-rebase-on-protected-branch", Type: TypeOnProtectedBranch, Args: []string{"git", "rebase"}},

		// free insurance: zero hits in 31 days, so these cost nothing.
		{Name: "sudo", Type: TypeCmd, Args: []string{"sudo"}},
		{Name: "eval", Type: TypeCmd, Args: []string{"eval"}},
		{Name: "shell-interpreter", Type: TypeCmd, Args: []string{"sh", "bash", "zsh"}},
		{Name: "git-clean", Type: TypeSub, Args: []string{"git", "clean"}},
		{Name: "remote-shell", Type: TypeCmd, Args: []string{"ssh", "scp", "sftp"}},
		{Name: "gh-pr-write", Type: TypeArgIn, Args: []string{"gh", "pr", "merge", "close"}},
		{Name: "gh-release", Type: TypeSub, Args: []string{"gh", "release"}},
		{Name: "gh-api-write", Type: TypeArgIn, Args: []string{"gh", "api", "POST", "PATCH", "PUT", "DELETE"}},
		{Name: "kubectl-write", Type: TypeSub, Args: []string{"kubectl", "apply", "delete", "scale", "rollout", "edit"}},
		{Name: "terraform-write", Type: TypeSub, Args: []string{"terraform", "apply", "destroy"}},

		// MCP writes. Deliberately narrow: save_* is normal planning workflow
		// and fires 173 times a month, so it is allowed.
		{Name: "mcp-destructive", Type: TypeToolRegex, Args: []string{`^mcp__.*__(delete|merge|share|submit)_.*`}},
	}

	for i := range rs {
		rs[i].Source = SourceDefault
	}

	return rs
}
