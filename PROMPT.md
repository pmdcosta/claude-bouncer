# `bouncer` — implementation brief

Single source of truth for building this. Everything below was decided and
measured before any code was written; the rule list in particular is empirical,
scored against 31 days of my real Claude Code session transcripts (4,596 Bash
calls, 1,071 Write/Edit, 173 MCP writes). **Do not "improve" the rules from
intuition** — if you think something is missing, say so in one sentence and let
me decide.

---

## What it is

A Go CLI that registers as a Claude Code `PermissionRequest` hook. Two outcomes:
**auto-approve**, or **let the normal prompt happen**. It never blocks anything.

It is a **noise cutter and an audit log**, not an auth layer. It is
allow-by-default and trivially sidesteppable — `eval`, a shell function or an
unusual spelling all get through, and that is accepted. Do not add blocking,
`deny` decisions, a `PreToolUse` hook, or evasion resistance. All three were
considered and cut.

### Why it exists

- **Noise.** With no allow-list, routine `grep` / `go build` / `git diff` calls
  prompt constantly on my personal account.
- **No record.** Claude Code only persists *denials*. There is no log of what
  was approved, so no way to see after the fact what actually ran.

Target: my **personal** Claude account, not the managed work account.

---

## Setup

The repo exists: `~/Workspace/claude-bouncer`, on `master`, one commit, remote
`git@github.com:pmdcosta/claude-bouncer.git`. Binary name `bouncer`.

Go 1.27. **These four dependencies and nothing else** — if you think you need
another, stop and ask; the answer is usually the standard library.

| | |
|---|---|
| `github.com/urfave/cli/v3` | CLI framework — v3.11.0 current, v3 is the active line |
| `mvdan.cc/sh/v3` | shell parsing |
| `gopkg.in/yaml.v3` | rules config |
| `github.com/stretchr/testify` | `require` in tests, test-only |

No logging library. No `github.com/pkg/errors`. No Makefile — `go build`,
`go test ./...` and `gofumpt` are enough.

**Ask me before the first commit** — branch first. Branch names: lowercase,
digits and hyphens only, never `/` or `_`. Commit messages: one sentence, two at
most, no bullet lists. PR title max 5–6 words; description is a short summary
plus a bullet list of key changes, no test-plan section.

---

## How I write Go

`~/.claude/rules/go.md` on this machine is my full style guide and should
auto-attach when you touch a `.go` file. **Read it and follow it.** The rules
below are the ones most often violated, not a complete list.

### Non-negotiable

- **Never use `else`.** Guard clauses first, return early, happy path hugs the
  left margin. The successful outcome is the last thing in the function.
- **Always wrap errors, with context.** Never a bare
  `if err != nil { return err }`. Message lowercase, no trailing period, in
  "failed to [verb]" form. Sentinel errors as package-level
  `var ErrNoRepo = errors.New("not in a git repo")`.

  > **Override:** `go.md` says use `errors.Wrap` from `github.com/pkg/errors`.
  > **Not in this repo.** Use the standard library:
  > `fmt.Errorf("failed to parse command: %w", err)`. Same message convention,
  > stdlib mechanism. This is the one place this brief deliberately contradicts
  > the style guide.
- **Named struct fields, always.** `Rule{Name: "rm", Type: TypeCmd}`, never
  positional.
- **No `panic`, no `init()`, no reflection, no `goto`.** No `any`/`interface{}`
  as lazy API design.
- **Accept interfaces, return structs.** Define them at the point of use, and
  only when the design demands a seam — never preemptively "for testing". One or
  two methods max.
- Make the zero value useful. No package-scoped mutable state; wire dependencies
  explicitly. Functional options if a constructor needs more than 3–4 parameters.
- Package names: short, lowercase, singular nouns. **Never** `util`, `common`,
  `helpers`, `base`, `misc`.
- Variable name length proportional to scope, describing contents not type:
  `rules` not `rulesSlice`, `ctx` not `context`.

### Tests

- **TDD where you can** — write the test, make it pass, refactor.
- **External test packages**: `package shellwalk_test`. Test through the public
  API.
- **`require`, never `assert`.** Cascading failures from `assert` are noise.
- Single case → test directly. Under four → subtests. Four or more →
  table-driven.
- `t.Parallel()` in subtests that share no state. `t.Helper()` in helpers.
- **Write a `FuzzXxx` for anything parsing untrusted input.** `internal/shellwalk`
  is exactly that — it parses arbitrary command strings. Seed with `f.Add` from
  the test matrix below. A fuzz target there is worth more than any other test
  in the repo.
- `go test -race ./...` must pass.

### Comments and output

- Doc comment on every exported symbol, and `// Package x does Y.` per package.
  Say **what** and **why**, never how.
- Comments inside function bodies are frequent, start lowercase, end with a
  period.
- `go.md` says structured logging (`zap`). That rule is about services. **This is
  a CLI: plain text on stdout/stderr, no logging library.** The audit log is
  JSONL written directly with `encoding/json`.

---

## How I want you to work

- **Think before coding.** State assumptions. If two readings are possible,
  present them — don't pick silently. If a simpler approach exists, say so and
  push back.
- **Simplicity first.** Minimum code that solves the problem. No features beyond
  what is asked, no abstractions for single-use code, no configurability nobody
  requested, no error handling for impossible states. If you write 200 lines and
  it could be 50, rewrite it.
- **Guard the dependency list and the scope.** Four dependencies. Prefer the
  standard library every time; a little copying beats a little dependency.
- **Verify, don't recall.** Check docs for library APIs rather than answering
  from memory. Read `mvdan.cc/sh/v3/syntax`'s actual API before designing around
  it.

---

## Build order

Stop and show me the result after each step.

1. **`internal/shellwalk`** — the hard part and the whole justification for the
   tool. Build and test-drive it first.
2. **`internal/rules`** — rule types, baked-in defaults, YAML loader with
   merge-by-name, `Match()`.
3. **`internal/hook`** + `decide` — stdin/stdout contract, golden tests.
4. **`internal/audit`** — JSONL, monthly files, 1 KB cap.
5. **`internal/settings`** + `enable` / `disable`.
6. **`rules`** and **`log`** subcommands.

### Layout

```
cmd/bouncer/main.go      # decide, enable, disable, rules, log, version
internal/hook/           # PermissionRequest stdin/stdout contract
internal/rules/          # baked defaults, YAML loader, merge-by-name, Match()
internal/shellwalk/      # AST walk + cwd tracking — densest tests in the repo
internal/audit/          # JSONL logger, monthly files, 1KB line cap
internal/settings/       # atomic, key-preserving settings.json edits
```

### Subcommands

| | |
|---|---|
| `decide` | the hook; reads JSON on stdin, writes a decision to stdout |
| `enable` | seed `~/.config/bouncer/rules.yaml` if absent, register the hook |
| `disable` | remove the hook entry; **leave `rules.yaml` alone** |
| `rules` | print the effective merged list, marked `[default]`/`[file]`/`[disabled]` |
| `rules --validate` | lint the config, exit non-zero on error |
| `log --since 7d --allowed` | filter the audit log |
| `version` | |

---

## The five things most likely to be got wrong

1. **`PermissionRequest` output is `decision.behavior`, NOT
   `permissionDecision`.** The latter is the `PreToolUse` shape. Using the wrong
   one fails silently — no error, the hook just does nothing. Pin the exact
   stdout bytes in a golden test.
2. **Never emit `allow` on an error path.** Malformed stdin, unparseable
   command, broken config, panic — all emit `ask`.
3. **Cap every audit line at 1 KB.** Several worktree sessions append to one
   file concurrently, and `O_APPEND` is only atomic below 4096 bytes.
4. **`settings.json` must decode into `map[string]any`, not a struct**, or
   `enable` silently eats keys it does not know about.
5. **Write the absolute path to the binary** in the hook command, not bare
   `bouncer`. Hooks do not inherit an interactive shell's `PATH`.

---

## The hook contract

Verified against [hooks.md](https://code.claude.com/docs/en/hooks.md),
[permissions.md](https://code.claude.com/docs/en/permissions.md), and the source
of `claude-remote-approver` (which uses this same event).

`PermissionRequest` fires when a tool call needs a permission decision — the
moment the prompt would appear — so a hook here can answer it. `PreToolUse`
cannot: its `allow` does not satisfy an `ask` rule and the prompt still shows.

- **stdin**: `session_id`, `cwd`, `hook_event_name`, `tool_name`, `tool_input`,
  `permission_suggestions`.
- **stdout**, exit 0:
  ```json
  {"hookSpecificOutput":{"hookEventName":"PermissionRequest",
    "decision":{"behavior":"allow"}}}
  ```
- `behavior` accepts `allow` / `deny` / `ask`. `ask` = show the normal prompt.
  We only ever emit `allow` and `ask`.
- Exit 2 is **not honoured** for this event.
- Non-zero exit or timeout = non-blocking error, the flow proceeds.
- Default timeout 600 s; per-hook `timeout` field. We set 10.
- Fires for subagent and MCP calls. For background subagents, **no decision =
  deny**, so auto-allow actively helps unattended runs.
- Hook errors surface only under `claude --debug` — hence the log file.

---

## Design

```
PermissionRequest ──▶ bouncer decide
                        ├─ any command in it matches an ask-rule ──▶ ask (prompt)
                        └─ otherwise                             ──▶ allow (silent)
```

### The walk — the one non-obvious piece

A Bash `command` is not one command. `cd /x && rm -f MEMORY.md` is two, and
matching only the first token is exactly why a `Bash(rm *)` permission rule never
caught the call that deleted my memory folder.

**Parse the command into an AST and walk it in execution order; escalate if any
command in it matches.** Parse with `mvdan.cc/sh/v3/syntax` rather than splitting
on separators, for two reasons:

1. **False positives.** A naive splitter breaks
   `git commit -m "fix things; remove cruft"` into a fake `remove cruft` command
   and prompts for nothing. Spurious prompts are exactly what this tool exists to
   remove, so quote handling has to be right.
2. **State.** Some rules need the cwd at each point, and cwd is changed by `cd`
   in earlier commands. That needs execution order and scope — a list of split
   strings cannot express it.

If the parser errors, escalate.

---

## Building the `rm` rule

The one genuinely hard rule, and the one the whole tool is justified by.

### Why argument inspection is not enough

```bash
cd /Users/pmdcosta/.claude/projects/…/memory && rm -f MEMORY.md
```

`MEMORY.md` is a bare relative filename. Nothing about the `rm` is suspicious.
The `cd` in the **previous** command is what left the repo. Any rule looking only
at `rm`'s arguments misses the exact incident it exists to prevent.

### The walker

```go
type cwdState struct {
    path    string // effective cwd for the next command
    unknown bool   // cwd can no longer be determined statically
}
```

Start from the hook's `cwd` field. Walk statements in order:

| Node | Effect on state |
|---|---|
| `cd <literal>` | resolve against current cwd, update |
| `cd` (no args) or `cd ~` | set to `$HOME` — **outside the repo**, a real case |
| `cd -`, `cd $VAR`, `pushd`/`popd` | `unknown = true` |
| `rm …` | **evaluate now**, against the current state |
| `( … )` subshell, `$( … )` substitution | recurse on a **copy**; discard its cwd changes — a `cd` in a subshell does not leak |
| `a \|\| b` | `cd` may or may not have run. Evaluate `b` under **both** states; ask if either is unsafe |
| `for` / `while` / `if` / function bodies | if the body contains any `cd`, set `unknown = true` for everything after |

`unknown = true` means every subsequent `rm` asks. Fail toward the prompt.

### Evaluating one `rm`

For each non-flag argument, stopping at the first that says ask:

1. **Unexpanded parameter** (`$VAR`, backtick, `$(…)` surviving the parse) →
   **ask**. Cannot know the target.
2. **Expand `~`** to `$HOME`.
3. **Resolve**: absolute stays as-is; relative joins the effective cwd.
4. **`filepath.Clean`** — catches `../../foo` escaping the repo.
5. **Globs** (`*`, `?`, `[`): a glob only matches within its own parent
   directory, so check the parent instead. `rm *.tmp` in-repo passes;
   `rm /etc/*` does not.
6. **Inside `repoRoot`?** If not → **ask**.

All arguments inside → **allow**. Otherwise → **ask**.

### Finding `repoRoot`

Walk up from the effective cwd looking for `.git`, as a **file or a directory** —
a worktree's `.git` is a file, and resolving to the worktree root is correct. No
`git` subprocess; a few filesystem stats.

If no `.git` is found above cwd, there is no repo boundary to be inside of →
**ask**.

### Do not resolve symlinks

`rm` on a symlink deletes the link, not its target. `filepath.EvalSymlinks` would
make an in-repo symlink pointing outside look like an outside delete and prompt
for something harmless. `Clean` only.

### One extra guard

`rm -rf .` at the repo root resolves *inside* the repo and would be allowed,
while wiping the working tree including untracked files and `.git`. Ask when a
resolved target **is** the repo root. Zero false positives, one real catastrophe
covered.

### Known gap, accepted

`find . -exec rm {} \;` and `… | xargs rm` never have `rm` as the leading word of
a command, so this rule does not see them. Consistent with the sidesteppable
stance; noted so it is a known gap rather than a surprise.

### Test matrix

`cwd` = `~/Workspace/insurance-mono`, `HOME` = `/Users/pmdcosta`.

| Command | Expect | Exercises |
|---|---|---|
| `rm -rf gen/proto` | allow | plain relative |
| `rm *.tmp` | allow | glob → parent dir |
| `cd services && rm x.go` | allow | `cd` stays in repo |
| `echo "cd /tmp && rm x"` | allow | **a string, not a command** |
| `cd ~/.claude/…/memory && rm -f MEMORY.md` | **ask** | **the real incident** |
| `rm -f "/Users/…/.claude/skills/x"` | ask | quoted absolute |
| `rm -rf ../../foo` | ask | `Clean` catches the escape |
| `rm -f $TARGET` | ask | unexpanded parameter |
| `cd $D && rm x` | ask | cwd unknown |
| `cd && rm x` | ask | bare `cd` → `$HOME` |
| `(cd /tmp && rm x); rm y` | ask, then allow | subshell cwd does not leak |
| `cd /tmp \|\| rm x` | ask | `\|\|` — evaluate both branches |
| `for d in a b; do cd $d; rm x; done` | ask | loop → unknown |
| `rm -rf .` at repo root | ask | the repo-root guard |

The `echo` row is the false-positive canary. A naive implementation prompts on
it, and one spurious prompt like that is enough to make the tool feel broken.

---

## The ask-list

Everything not listed is auto-approved. Measured monthly prompt cost in the
`fires/mo` column. **Total: ~58 prompts a month, about 2 a day.**

### Core

| Rule | type | fires/mo | Why |
|---|---|---|---|
| `rm` **outside the repo** | `args_outside_repo` | ~6 | The incident above. In-repo `rm` is allowed. |
| config paths | `path_glob` | 15 | below |
| `curl`, `wget` | `cmd` | 14 | Network egress. |
| `git reset --hard` | `flag` | 7 | Discards uncommitted work. |
| `git stash` (all forms) | `sub` | 7 | Stack is shared across my worktrees. |
| `go install`, `npm\|yarn\|pnpm install\|add`, `brew install` | `sub` | 3 | Supply chain. |
| `go mod` | `sub` | 2 | |
| `chmod`, `chown` | `cmd` | 2 | |
| `git branch -D` | `flag` | 1 | |
| `git config` | `sub` | 1 | |
| `git push` — three rules | mixed | ~0 | below |
| `git rebase` while **on** main/master | `on_protected_branch` | ~0 | below |

### The three narrowed rules

As flat command matches these were `rm` 14/mo, `git push` 72/mo, `git rebase`
29/mo. Narrowing by target cut 115 prompts a month to ~6, and made two of them
*more* accurate, not less.

**`git push` — three separate entries, OR'd by both existing:**

| | |
|---|---|
| naming `main`/`master` | ask — 0/mo |
| with `--force` or `-f` | ask — 0/mo. **`--force-with-lease` is allowed**: it refuses if the remote moved, and it is my standard PR-update workflow (16/mo) |
| while `.git/HEAD` is on `main`/`master` | ask — covers bare `git push` (16/mo, none on main) |

**`git rebase` — the current branch matters, not the target.** Of 29 calls, 17
are `--continue`/`--abort`/`--skip` and 12 are `git rebase origin/main` —
rebasing a *feature branch onto* main, normal daily work. Only rebasing while
**on** main/master is worth a prompt. Read `.git/HEAD`, no subprocess.

### Free insurance — zero hits in 31 days

`sudo`, `eval`, pipe-into-shell, `git clean`, `ssh`, `scp`, `sftp`,
`gh pr merge|close`, `gh release`, `gh api -X POST|PATCH|PUT|DELETE`,
`kubectl apply|delete|scale|rollout|edit`, `terraform apply|destroy`.

### Config paths (`path_glob`)

`**/settings.json`, `**/settings.local.json`, `**/.claude/hooks/**`, `**/.env*`,
`**/*strongbox-keyring`, `~/.ssh/**`, `~/.aws/**`, `~/.kube/**`,
`~/.config/bouncer/**`.

**Bouncer must not silently rewrite its own rules** — that is why its own config
directory is on this list.

### MCP

`mcp__.*__(delete|merge|share|submit)_.*` only. Zero hits in 31 days.

### Deliberately allowed — do not add these

| | /mo | |
|---|---|---|
| `git commit`, `git add`, `git checkout` | 176 | local and reversible |
| anything outside `~/Workspace` | 318 | almost all Claude's own `~/.claude/plans/*.md` |
| `mcp__.*__save_.*` | 173 | `save_document` / `save_issue`, normal planning workflow |

The last two were in an earlier draft and the measurement killed them. That is
the argument for building `bouncer log` early: two of twenty rules were badly
wrong and only the data showed it.

---

## Where the rules live

**Baked-in defaults in Go, overridable by `~/.config/bouncer/rules.yaml`**
(respect `XDG_CONFIG_HOME`). The file is optional; with none present, bouncer
runs off the compiled list.

YAML rather than JSON because a rule list needs comments.

### Rule types — a fixed vocabulary, so the file can be validated

| `type` | Args | Example |
|---|---|---|
| `cmd` | command name | `rm`, `sudo` |
| `sub` | command + subcommand | `git push`, `kubectl apply` |
| `flag` | command + subcommand + flag | `git reset --hard` |
| `arg_in` | command + sub + values | `git push` naming `main`/`master` |
| `path_glob` | glob | `~/.ssh/**`, `**/.env*` |
| `args_outside_repo` | command | `rm` — args resolved against the effective cwd |
| `on_protected_branch` | command [+ sub] | `git push`, `git rebase` — reads `.git/HEAD` |
| `tool_regex` | regex on `tool_name` | `mcp__.*__(delete\|merge\|share\|submit)_.*` |

`path_glob`, `args_outside_repo` and `on_protected_branch` need real path and git
logic, so they live in Go. The YAML only names them and supplies arguments. Two
rules on the same command are simply two entries — that is how `git push` gets
its three independent conditions without the file needing boolean operators.

```yaml
rules:
  - name: rm-outside-repo
    type: args_outside_repo
    args: [rm]
  - name: git-reset-hard
    type: flag
    args: [git, reset, --hard]
  - name: git-config          # a default I don't want
    enabled: false
```

### Merge semantics — by `name`

A file entry whose `name` matches a default **replaces** it; a new `name` is
**added**; `enabled: false` **switches a default off**. Whole-file replacement
was the alternative and is worse: the file would silently miss any new defaults
added in a later version.

### Failure handling

A bad regex or malformed YAML fails at *runtime*, and hook errors only surface
under `claude --debug`. Left alone, a typo means bouncer silently stops catching
a command and nobody finds out. So:

1. **Any load or validation error → fall back to the baked-in defaults**, whole.
   Never partial, never empty. The compiled list is the safety floor.
2. **Write the error to the audit log and to stderr.**
3. **`bouncer rules`** prints the effective merged list with a
   `[default]`/`[file]`/`[disabled]` marker per row — how you confirm what is
   actually live rather than what you think you wrote.
4. **`bouncer rules --validate`** lints and exits non-zero. `enable` runs it too.

---

## Audit log

**JSONL, one file per month**: `~/.claude/logs/bouncer-2026-09.jsonl`.

~7,500 tool calls a month at ~400 bytes a line is **~3 MB a month** — far too
small to justify SQLite, which would also cost the static binary (cgo) or ~5 MB
of pure-Go driver, plus 1–3 ms on every call against a ~5 ms budget.

```json
{"ts":"2026-09-07T14:22:19Z","session":"5d6c09b1","cwd":"/Users/…/insurance-mono",
 "tool":"Bash","input":"cd /x && rm -f MEMORY.md","outcome":"ask","rule":"rm-outside-repo"}
```

`rule` is the pattern that matched, empty for a default-allow.

Three constraints:

1. **Cap each line at 1 KB** (truncate `input`). Several worktree sessions append
   to the same file concurrently, and POSIX guarantees atomic `O_APPEND` writes
   only below 4096 bytes — over that, lines interleave and corrupt. This is the
   reason for the cap, not tidiness.
2. **Monthly files, no rotation code.** `--since` becomes file selection.
3. **Recover around every write.** A failed log write must never change the
   decision.

`bouncer log --since 7d --allowed` is a thin filter over the monthly files. Plain
`jq` stays a first-class way in — this is how the ask-list gets tuned:

```bash
jq -r 'select(.outcome=="allow").input' bouncer-2026-09.jsonl \
  | sort | uniq -c | sort -rn | head
```

---

## `enable` / `disable`

`enable` turns bouncer on; `disable` turns it off and leaves tuning intact.

### `bouncer enable`

1. **Seed `~/.config/bouncer/rules.yaml` if absent** — create the directory,
   write the starter file, `0600`.
2. **Register the hook** in `~/.claude/settings.json`.
3. **Print what it did**, including the effective rule count.

Existing file or hook already present: leave it and say so. Running `enable`
twice must be a no-op, never a duplicate entry — match on the handler's command
containing `bouncer`.

Two guards, both refusing with a non-zero exit:

- **`claude-remote-approver` is registered.** Two handlers on `PermissionRequest`
  race, and the docs do not define how competing decisions resolve. Print the
  exact fix (`claude-remote-approver uninstall`) rather than quietly creating the
  race.
- **`rules.yaml` fails validation.** Never enable into a broken config.

### The seeded file is comments, not a copy of the defaults

```yaml
# bouncer rules — github.com/pmdcosta/claude-bouncer
# Defaults are compiled into the binary. This file only *changes* them.
# See the live list with:  bouncer rules
#
# rules:
#   - name: my-rule          # add one
#     type: cmd
#     args: [terraform]
#
#   - name: git-config       # switch a default off
#     enabled: false

rules: []
```

Writing the full default list out would work — merge-by-name is upgrade-safe
either way — but it duplicates every rule into a file you then keep in sync by
eye. Starting empty means the binary stays the single source of truth and the
file records only deltas.

### `bouncer disable`

Removes the hook entry, preserving every other hook and setting. If it was the
only `PermissionRequest` handler, drop the now-empty key rather than leaving
`"PermissionRequest": []`.

**It does not touch `rules.yaml`.** Disable is a switch, not an uninstall — I
should be able to flip it off and back on without losing tuning. A `--purge` flag
can delete the config for people who really mean it.

### The hook entry

```json
"PermissionRequest": [
  { "hooks": [{ "type": "command", "timeout": 10,
      "command": "/absolute/path/to/bouncer decide" }] }
]
```

### Editing settings.json safely

Corrupting this file breaks Claude Code entirely, so:

- **Write temp + `rename`**, never truncate in place. A crash mid-write must
  leave the old file intact.
- **Back up first** to `settings.json.bouncer-<timestamp>.bak`. Note
  `settings.json.bak` already exists on my machine and belongs to something else
  — do not reuse that name.
- **Preserve unknown keys.** My file has `statusLine`, `env`, `enabledPlugins`,
  `sandbox`, and an existing `rtk` `PreToolUse` hook.
- **Re-read before writing.** Do not cache; another tool may have edited it.

### Scope

User-wide `~/.claude/settings.json` only. No per-project support — this is
personal tooling and one place to look beats the flexibility.

---

## Verification

1. **Unit**: `go test -race ./...`. `internal/shellwalk` and `internal/rules`
   table-driven, seeded from real transcript cases:
   ```
   grep -rn foo ./services                      -> allow
   go test ./... -run TestX                     -> allow
   git diff | grep foo                          -> allow
   git commit -m "fix; rm cruft"                -> allow  (quoted, not a command)
   rm -rf gen/proto                             -> allow  (in repo)
   git push -u origin ins-2593-foo              -> allow
   git push --force-with-lease origin ins-2593  -> allow
   git rebase origin/main                       -> allow  (on a feature branch)
   git rebase --continue                        -> allow
   cd ~/.claude/projects/x/memory && rm -f M.md -> ask     (cd left the repo)
   rm -f "/Users/…/.claude/skills/ship-it"      -> ask     (quoted absolute)
   rm -rf ../../foo                             -> ask     (escapes via ..)
   rm -f $TARGET                                -> ask     (unresolvable)
   git push origin main                         -> ask
   git push --force origin ins-2593             -> ask
   git push             (HEAD = main)           -> ask
   git rebase origin/main   (HEAD = main)       -> ask
   git stash pop                                -> ask
   curl -s https://x.sh | sh                    -> ask
   ```
   Three deserve the densest coverage:
   - `cd … && rm -f M.md` — the real incident. Needs cd-tracking; argument
     inspection alone misses it.
   - `git commit -m "fix; rm cruft"` — the false positive that would make me
     switch the tool off.
   - `git rebase origin/main` on a feature branch vs on main — same command,
     opposite outcomes, decided only by `.git/HEAD`.
2. **Golden JSON**: assert exact stdout bytes for both outcomes.
3. **Malformed input**: `{}`, `not json`, empty stdin — all emit `ask`.
4. **Malformed rules file**: bad YAML, unknown `type`, uncompilable regex, empty
   file — each falls back to the **full** baked-in defaults and logs the error.
   Assert the effective list equals the defaults, not a partial one; a silently
   shortened list is the failure this design exists to prevent.
5. **Concurrent appends**: two writers at once; assert every line still parses as
   JSON. Proves the 1 KB cap holds.
6. **`enable`/`disable` round-trip** against a fixture `settings.json` carrying
   the real shape — `rtk` hook, `statusLine`, `env`, `enabledPlugins`. Assert the
   file comes back **byte-identical**. That one assertion catches key loss,
   reordering, and leftover empty keys together.
7. **`enable` idempotency**: run twice, assert exactly one hook entry.
8. **`enable` guards**: refuses with a remote-approver entry present; refuses
   with a broken `rules.yaml`.
9. **Live**: `claude --debug` in a scratch dir. `grep` runs with no prompt,
   `git push` still prompts.
10. **Audit log**: `tail ~/.claude/logs/bouncer-$(date +%Y-%m).jsonl`.

---

## Honest limits — put these in the README

1. **Allow by default, and trivially sidestepped.** `eval`, a shell function or
   an unusual spelling all get through. By design. Review the audit log.
2. **Fail-open.** A crash or timeout means the normal prompt. Benign here.
3. **Not a security control.** It cannot block anything. The OS sandbox and
   Claude Code's own permission rules are the actual boundary.
4. On a managed work account a hook `allow` cannot beat a managed `deny` or
   `ask`. Deny-first precedence always holds.

---

## Do not

- **Do not run `enable` / `disable` yourself.** `~/.claude/settings.json` is
  write-denied in the sandbox, and it is my call when this goes live.
- **Do not add** a `PreToolUse` hook, hard blocks, `deny` decisions, or
  phone/remote approval. All were considered and cut.
- **Do not widen the rule list from intuition.** It was measured.
