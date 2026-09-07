# skills

Claude Code skills for working with bouncer, kept here for version control.

They are **not** loaded from this directory. Claude Code only picks skills up
from `~/.claude/skills` (personal) or `.claude/skills` (per-project), so this
copy is the tracked original and the personal one is what actually runs.

That is deliberate: a copy under `.claude/skills` here would load as a *project*
skill on top of the personal one, and once the two drifted the stale repo copy
would shadow the personal one inside this repo only — which is a confusing way
to find out they are out of sync.

## Install or update

```bash
mkdir -p ~/.claude/skills/bouncer-prompts && cp -R skills/bouncer-prompts/. ~/.claude/skills/bouncer-prompts/
```

Safe to re-run. It overwrites what changed and deletes nothing.

## Check for drift

Run this after editing either copy, and before committing:

```bash
diff -r skills/bouncer-prompts ~/.claude/skills/bouncer-prompts
```

No output means they match. To stop them drifting at all, replace the personal
copy with a link — at the cost of the skill disappearing if this repo moves:

```bash
ln -s "$PWD/skills/bouncer-prompts" ~/.claude/skills/bouncer-prompts
```

## What is here

| | |
|---|---|
| `bouncer-prompts/` | works out why a permission prompt appeared, and helps narrow or switch off the rule behind it |

`bouncer-prompts` needs `bouncer` on `PATH`, including the `explain` subcommand:

```bash
go install ./cmd/bouncer
```
