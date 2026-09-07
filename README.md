# bouncer

A Go CLI that registers as a Claude Code `PermissionRequest` hook. Two
outcomes: **auto-approve**, or **let the normal prompt happen**. It never
blocks anything.

It exists for two reasons:

- **Noise.** With no allow-list, routine `grep` / `go build` / `git diff` calls
  prompt constantly.
- **No record.** Claude Code only persists *denials*. Without this there is no
  log of what was approved, so no way to see after the fact what actually ran.

```
PermissionRequest ──▶ bouncer decide
                        ├─ any command in it matches an ask-rule ──▶ prompt
                        └─ otherwise                             ──▶ allow (silent)
```

## Install

```bash
go install github.com/pmdcosta/claude-bouncer/cmd/bouncer@latest
```

Then turn it on. `enable` seeds `~/.config/bouncer/rules.yaml` if it is absent
and registers the hook in `~/.claude/settings.json`, using the absolute path to
the binary because hooks do not inherit an interactive shell's `PATH`.

```bash
bouncer enable
```

Running `enable` twice is a no-op, never a duplicate entry. It refuses if
`claude-remote-approver` is already registered for the same event, and it
refuses to enable into a `rules.yaml` that fails validation.

## Commands

| | |
|---|---|
| `bouncer decide` | the hook; reads JSON on stdin, writes a decision to stdout |
| `bouncer enable` | seed the rules file if absent, register the hook |
| `bouncer disable` | remove the hook entry; leaves `rules.yaml` alone |
| `bouncer disable --purge` | also delete the rules file |
| `bouncer rules` | print the effective merged list, marked `[default]`/`[file]`/`[disabled]` |
| `bouncer rules --validate` | lint the config, exit non-zero on error |
| `bouncer log --since 7d --asked` | filter the audit log |
| `bouncer log --full` | print each command in full instead of on one row |
| `bouncer --version` | |

`disable` is a switch, not an uninstall: flip it off and back on without losing
any tuning.

## The walk

A Bash `command` is not one command. `cd /x && rm -f MEMORY.md` is two, and
matching only the first token is exactly why a `Bash(rm *)` permission rule
never caught the call that deleted a memory folder.

bouncer parses the command into a shell AST and walks it in execution order,
tracking the working directory as `cd` moves it, and escalates if **any**
command in it matches a rule. Parsing rather than splitting on separators
matters twice over:

- `git commit -m "fix things; remove cruft"` is one command, not two. A naive
  splitter invents a `remove cruft` command and prompts for nothing. Spurious
  prompts are exactly what this tool exists to remove.
- Some rules need the working directory at each point, and `cd` in an earlier
  command changes it. A list of split strings cannot express that.

If the parser errors, bouncer prompts.

## Rules

The default list is compiled into the binary and is **empirical**: it was
scored against 31 days of real session transcripts and costs roughly 58 prompts
a month, about two a day. `~/.config/bouncer/rules.yaml` only *changes* it.

```yaml
rules:
  - name: my-rule          # add one
    type: cmd
    args: [terraform]

  - name: git-config       # switch a default off
    enabled: false
```

Merging is by `name`: a matching name replaces a default, a new name is added,
and `enabled: false` switches a default off. That keeps the file upgrade-safe —
a new default in a later version still arrives.

### Rule types

| `type` | Args | Example |
|---|---|---|
| `cmd` | command names | `rm`, `sudo` |
| `sub` | command + subcommands | `git stash` |
| `flag` | command + subcommand + flags | `git reset --hard` |
| `arg_in` | command + subcommand + values | `git push` naming `main` |
| `path_glob` | globs, with `**` spanning directories | `~/.ssh/**`, `**/.env*` |
| `args_outside_repo` | one command | `rm` — operands resolved against the effective cwd |
| `on_protected_branch` | command [+ subcommand] | `git push`, `git rebase` — reads `.git/HEAD` |
| `tool_regex` | regexes on the tool name | `^mcp__.*__(delete\|merge)_.*` |

Two conditions on the same command are simply two entries. That is how
`git push` gets three independent conditions — naming `main`, carrying
`--force`, or being run while HEAD is on `main` — without the file needing
boolean operators. `--force-with-lease` is deliberately allowed: it refuses if
the remote moved, and it is the standard PR-update workflow.

### If the file is broken

A bad regex or malformed YAML fails at *runtime*, and hook errors surface only
under `claude --debug`. Left alone, a typo would mean bouncer silently stops
catching a command and nobody finds out. So **any** load or validation error
falls back to the compiled defaults **whole** — never partial, never empty —
and writes the error to the audit log and to stderr. `bouncer rules` is how you
confirm what is actually live rather than what you think you wrote.

## Audit log

JSONL, one file per month, at `~/.claude/logs/bouncer-YYYY-MM.jsonl`.

```json
{"ts":"2026-09-07T14:22:19Z","session":"5d6c09b1","cwd":"/Users/p/Workspace/example-repo","tool":"Bash","input":"cd /x && rm -f MEMORY.md","outcome":"ask","rule":"rm-outside-repo"}
```

`rule` is the rule that matched, and is empty for a default allow. Each line is
capped at 1 KB: several worktree sessions append to the same file at once, and
POSIX guarantees an atomic `O_APPEND` write only below 4096 bytes.

`bouncer log` is a thin filter over these files. It prints one row per record:
every run of whitespace in a command becomes a single space and the row is cut
to the terminal width, because a twenty-line heredoc otherwise destroys the
columns. `--full` prints each command as it was written instead.

```
09-07 15:36  allow  -              Bash  grep -rn foo .
09-07 15:36  allow  -              Bash  cd "$TMPDIR" && python3 - <<PY import json,os,base…
09-07 15:36  ask    git-stash      Bash  git stash pop
09-07 15:36  ask    network-fetch  Bash  curl -s https://example.sh | sh
```

Output is coloured when it goes to a terminal — green allowed, yellow prompted,
cyan for the rule that matched. Redirect it, pipe it, or set `NO_COLOR` and it
is plain text. `COLUMNS` sets the width.

Plain `jq` stays a first-class way in — the file always holds the full command,
and this is how the ask-list gets tuned:

```bash
jq -r 'select(.outcome=="allow").input' ~/.claude/logs/bouncer-2026-09.jsonl | sort | uniq -c | sort -rn | head
```

Two of the first twenty rules turned out to be badly wrong, and only the data
showed it.

## Honest limits

1. **Allow by default, and trivially sidestepped.** `eval`, a shell function,
   an unusual spelling, `find -exec rm` and `xargs rm` all get through. By
   design. Review the audit log.
2. **Fail-open.** A crash or a timeout means the normal prompt. Benign here.
3. **Not a security control.** It cannot block anything. The OS sandbox and
   Claude Code's own permission rules are the actual boundary.
4. **A hook `allow` never beats a managed `deny` or `ask`.** Deny-first
   precedence always holds, so on a managed account this changes nothing.
5. **`rm` is judged against the nearest enclosing git repository.** If a
   directory is itself inside a repo — a dotfiles repo covering `$HOME`, say —
   then paths under it count as in-repo.

## Development

```bash
go test -race ./...
go test ./internal/shellwalk/ -run=NONE -fuzz=FuzzWalk -fuzztime=60s
```

`internal/shellwalk` parses arbitrary command strings, so its fuzz target is
worth more than any other test here — it found a real bug within twelve
seconds of first being run.
