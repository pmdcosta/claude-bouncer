---
name: bouncer-prompts
description: >-
  Diagnose why Claude Code showed a permission prompt, using the bouncer audit
  log and `bouncer explain`, then narrow or switch off the rule that caused it
  in ~/.config/bouncer/rules.yaml or in bouncer's compiled defaults. Use this
  whenever the user asks why they were just prompted, why a permission request
  appeared, what rule matched a command, how to stop being asked about a kind
  of command, or wants to add, narrow, disable, or tune a bouncer rule. It
  covers phrasings like "why did that prompt me", "check the last permission
  request", "stop asking me about X", "which rule caught this", "add a rule so
  this doesn't prompt again", and any mention of bouncer, rules.yaml, or the
  ask-list. Reach for it even when the user does not name bouncer, as long as
  they are asking about a permission prompt they just saw.
---

# Why did bouncer prompt me?

## What bouncer is

A `PermissionRequest` hook. For every tool call that would prompt, it either
auto-approves silently or stays out of the way and lets the prompt happen. It
never blocks. Two outcomes only: `allow` and `ask`.

The default rule list is **empirical** — scored against 31 days of real
transcripts and costed at roughly 58 prompts a month. That measurement is the
whole value of the list. Treat it as data, not taste: when you change it, you
should be able to say what the change costs in prompts.

Where things live:

| | |
|---|---|
| audit log | `~/.claude/logs/bouncer-YYYY-MM.jsonl`, one file per month |
| personal rules | `~/.config/bouncer/rules.yaml` (respects `XDG_CONFIG_HOME`) |
| compiled defaults | `~/Workspace/claude-bouncer/internal/rules/defaults.go` |

## Step 1 — find the prompt

```bash
python3 ~/.claude/skills/bouncer-prompts/scripts/asks.py recent --since 1d
```

This lists recent prompts newest first, and for each one prints a ready-made
`bouncer explain` command with the full text already in a file, so there is no
shell quoting to get wrong.

If the user means "the one I just saw", it is normally `[1]`. Say which record
you picked so they can correct you.

**A record marked TRUNCATED cannot be replayed.** Log lines are capped at 1 KB,
so a long command loses its tail — and a cut-off heredoc or quote fails to
parse, which makes `bouncer explain` report a parse error rather than the real
reason. When you hit one, ask the user for the full command, or find it earlier
in the session, before drawing any conclusion.

## Step 2 — work out why

```bash
bouncer explain --cwd <dir> - < <the file asks.py wrote>
```

`explain` answers the same question the hook does, but writes no audit record,
so diagnosing a prompt never pollutes the log you are reading.

Read its output as three things: the verdict, the rule that fired, and **the
walk**. The walk is usually where the answer is, because a rule judges one
simple command at one effective working directory, and neither of those is
visible in the text the user typed. `cd /elsewhere && rm -f x` is two commands,
and the `cd` is what makes the `rm` unsafe.

`->` marks the command that matched. A long line can hold a dozen simple
commands, so quote that one back to the user rather than the whole line — it is
the actual answer. The state line under it is only repeated where it changes, so
a restated `cwd` means an earlier `cd` moved it.

There are five reasons a prompt appears. Work down the list:

1. **A rule matched.** `explain` names it with its type and arguments. Say
   which command in the walk it matched and which argument did it.
2. **bouncer could not parse the command.** The log record has an `error` and
   no rule. It fails toward the prompt on purpose. The fix is not a rule
   change — it is usually a genuinely odd command, or a truncated log line.
3. **The rules file is broken.** `explain` prints `rules file problem`. Any
   error there falls back to the *full* compiled defaults, silently, so a typo
   makes bouncer quietly stop catching something. Fix the file first.
4. **bouncer said `allow` but a prompt appeared anyway.** Then bouncer is not
   the cause. A hook `allow` never beats Claude Code's own `deny`/`ask` rules
   or the Bash sandbox — deny-first precedence always holds. Check
   `permissions` in `~/.claude/settings.json` and the sandbox config. Do not
   change a bouncer rule for this; it will not help.
5. **Nothing is in the log at all.** bouncer may not be registered. Check
   `bouncer rules` runs, and that `hooks.PermissionRequest` in
   `~/.claude/settings.json` points at an absolute path.

## Step 3 — decide the fix

First, get the cost:

```bash
python3 ~/.claude/skills/bouncer-prompts/scripts/asks.py counts --since 30d
```

Then read the actual commands behind the top rule with `recent`. A rule at the
top is either earning its keep or misfiring, and only the commands tell you
which. **Do not skip this.** One annoying prompt feels like a broken rule; the
log is what shows whether it is.

### Which file?

**Default to `rules.yaml`.** It is personal tuning, it cannot affect anyone
else, and merging by name keeps it upgrade-safe — a new default in a later
version still arrives.

**Only touch `defaults.go`** when the rule is wrong for *everyone*, and you can
point at counts from the log to show it. That is a code change in the repo, on
a branch, with a test in `internal/rules/rules_test.go` proving the new
behaviour. The brief for this tool says plainly: do not widen the rule list
from intuition, it was measured. Honour that.

### Narrow before you disable

A rule that fires on the wrong thing often still guards something real.
Replacing it by name with a tighter version keeps the protection:

```yaml
rules:
  # the default is [curl, wget]; wget is never used here.
  - name: network-fetch
    type: cmd
    args: [curl]
```

Switching a rule off entirely is right when the *intent* cannot be said in the
type vocabulary at all. `shell-interpreter` is the example: it exists to catch
`curl … | sh`, but no type can express "piped into a shell", so it is written
as `cmd [sh, bash, zsh]` and it also catches every `bash ./script.sh`. When
that is the situation, **say so** rather than inventing a rule that
approximates the intent badly:

```yaml
rules:
  - name: shell-interpreter
    enabled: false
```

### Adding a new rule

Two conditions on one command are two separate entries — that is how
`git push` gets three independent conditions with no boolean operators in the
file. Pick the narrowest type that says what you mean:

| `type` | Args | Matches |
|---|---|---|
| `cmd` | command names | the command name, e.g. `sudo` |
| `sub` | command + subcommands | first operand, e.g. `git stash` |
| `flag` | command + sub + flags | an exact flag; `--force` never matches `--force-with-lease` |
| `arg_in` | command + sub + values | any operand, e.g. `git push` naming `main` |
| `path_glob` | globs, `**` spans directories | file tool paths, every command argument, and redirect targets |
| `args_outside_repo` | one command | operands resolving outside the enclosing git repo |
| `on_protected_branch` | command [+ sub] | run while `.git/HEAD` is on `main`/`master` |
| `tool_regex` | regexes | the tool name, e.g. `^mcp__.*__delete_` |

## Step 4 — propose, then apply

Show the exact edit and what it will cost or save in prompts, then wait for a
yes. Do not edit `rules.yaml` unasked.

There is a reason beyond politeness: `~/.config/bouncer/**` is on bouncer's own
ask-list, precisely so the tool cannot silently rewrite its own rules. So the
edit will prompt. That is the design working, not a problem to route around.

## Step 5 — verify

Three checks, in order. None is optional, because a mistake here fails silently.

```bash
bouncer rules --validate                 # exits non-zero on any error
bouncer rules | grep <rule-name>         # confirm [file] or [disabled]
bouncer explain --cwd <dir> - < <file>   # the original command now allows
```

The middle one matters most: it prints what is **actually live**, rather than
what you think you wrote. If validation failed, the live list is the full
compiled defaults and your file is doing nothing at all.

Finish by saying what changed and what it costs — "this rule was 5 of your 10
prompts this week; disabling it drops those, and gives up catching
`curl | sh`, which the network-fetch rule already catches anyway."

## Worked example

> "why did it just ask me about that bash command?"

```
[2] 2026-09-07 15:47  ask  rule=shell-interpreter  tool=Bash
    command: SP=/tmp/scratch; bash $SP/demo.sh $SP/bouncer 2>&1 | head -5
```

```
ask  shell-interpreter
  rule: cmd [sh bash zsh]  from default

commands found, in execution order (-> is the one that matched):
   1. env <unexpanded> COLUMNS=140 <unexpanded> rules
      cwd /Users/p/Workspace/claude-bouncer, repo /Users/p/Workspace/claude-bouncer, branch master
   2. head -6
-> 3. bash <unexpanded> <unexpanded>
      cwd /Users/p/Workspace/claude-bouncer, repo /Users/p/Workspace/claude-bouncer, branch master
   4. head -5
```

The answer: `shell-interpreter` matched the command name `bash` in command 3. Counts showed
it caused 5 of 10 prompts that week, and `recent` showed all five were
`bash <local script>` — not one was a pipe into a shell, which is what the rule
was for. The intent is not expressible as a `cmd` rule, and `curl | sh` is
already caught by `network-fetch`, so switching it off loses nothing real:

```yaml
rules:
  - name: shell-interpreter
    enabled: false
```
