package shellwalk_test

import (
	"testing"

	"github.com/pmdcosta/claude-bouncer/internal/shellwalk"
	"github.com/stretchr/testify/require"
)

const (
	home = "/Users/pmdcosta"
	repo = "/Users/pmdcosta/Workspace/insurance-mono"
)

// want describes one expected command in a compact form, so the table below
// stays readable.
type want struct {
	name string
	args []string
	cwd  string
}

// flat renders the walked commands in the same compact form as want, using a
// "?" marker for an argument that could not be expanded.
func flat(t *testing.T, cmds []shellwalk.Command) []want {
	t.Helper()

	got := make([]want, 0, len(cmds))
	for _, c := range cmds {
		args := make([]string, 0, len(c.Args))
		for _, a := range c.Args {
			if !a.Expanded {
				args = append(args, "?")
				continue
			}
			args = append(args, a.Value)
		}
		got = append(got, want{name: c.Name, args: args, cwd: c.Cwd})
	}

	return got
}

func TestWalk(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []want
	}{
		{
			name:    "plain relative",
			command: "rm -rf gen/proto",
			want:    []want{{name: "rm", args: []string{"-rf", "gen/proto"}, cwd: repo}},
		},
		{
			name:    "glob argument is kept verbatim",
			command: "rm *.tmp",
			want:    []want{{name: "rm", args: []string{"*.tmp"}, cwd: repo}},
		},
		{
			name:    "cd threads into the next command",
			command: "cd services && rm x.go",
			want: []want{
				{name: "cd", args: []string{"services"}, cwd: repo},
				{name: "rm", args: []string{"x.go"}, cwd: repo + "/services"},
			},
		},
		{
			name:    "quoted text is a string not a command",
			command: `echo "cd /tmp && rm x"`,
			want:    []want{{name: "echo", args: []string{"cd /tmp && rm x"}, cwd: repo}},
		},
		{
			name:    "the real incident",
			command: "cd ~/.claude/projects/x/memory && rm -f MEMORY.md",
			want: []want{
				{name: "cd", args: []string{home + "/.claude/projects/x/memory"}, cwd: repo},
				{name: "rm", args: []string{"-f", "MEMORY.md"}, cwd: home + "/.claude/projects/x/memory"},
			},
		},
		{
			name:    "quoted absolute path",
			command: `rm -f "/Users/pmdcosta/.claude/skills/x"`,
			want:    []want{{name: "rm", args: []string{"-f", home + "/.claude/skills/x"}, cwd: repo}},
		},
		{
			name:    "dot dot is not resolved by the walker",
			command: "rm -rf ../../foo",
			want:    []want{{name: "rm", args: []string{"-rf", "../../foo"}, cwd: repo}},
		},
		{
			name:    "unexpanded parameter",
			command: "rm -f $TARGET",
			want:    []want{{name: "rm", args: []string{"-f", "?"}, cwd: repo}},
		},
		{
			name:    "cd to a parameter makes cwd unknown",
			command: "cd $D && rm x",
			want: []want{
				{name: "cd", args: []string{"?"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: ""},
			},
		},
		{
			name:    "bare cd goes home",
			command: "cd && rm x",
			want: []want{
				{name: "cd", args: []string{}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: home},
			},
		},
		{
			name:    "cd tilde goes home",
			command: "cd ~ && rm x",
			want: []want{
				{name: "cd", args: []string{home}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: home},
			},
		},
		{
			name:    "cd dash makes cwd unknown",
			command: "cd - && rm x",
			want: []want{
				{name: "cd", args: []string{"-"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: ""},
			},
		},
		{
			name:    "subshell cwd does not leak",
			command: "(cd /tmp && rm x); rm y",
			want: []want{
				{name: "cd", args: []string{"/tmp"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: "/tmp"},
				{name: "rm", args: []string{"y"}, cwd: repo},
			},
		},
		{
			name:    "command substitution cwd does not leak",
			command: "echo $(cd /tmp && rm x); rm y",
			want: []want{
				{name: "cd", args: []string{"/tmp"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: "/tmp"},
				{name: "echo", args: []string{"?"}, cwd: repo},
				{name: "rm", args: []string{"y"}, cwd: repo},
			},
		},
		{
			name:    "or evaluates the right side under both states",
			command: "cd /tmp || rm x",
			want: []want{
				{name: "cd", args: []string{"/tmp"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: "/tmp"},
			},
		},
		{
			name:    "loop with a cd makes cwd unknown",
			command: "for d in a b; do cd $d; rm x; done",
			want: []want{
				{name: "cd", args: []string{"?"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: ""},
			},
		},
		{
			name:    "cd inside a loop body poisons later commands",
			command: "for d in a b; do cd /tmp; done; rm x",
			want: []want{
				{name: "cd", args: []string{"/tmp"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: ""},
			},
		},
		{
			name:    "repo root target",
			command: "rm -rf .",
			want:    []want{{name: "rm", args: []string{"-rf", "."}, cwd: repo}},
		},
		{
			name:    "pipeline",
			command: "git diff | grep foo",
			want: []want{
				{name: "git", args: []string{"diff"}, cwd: repo},
				{name: "grep", args: []string{"foo"}, cwd: repo},
			},
		},
		{
			name:    "semicolon inside a quoted commit message",
			command: `git commit -m "fix; rm cruft"`,
			want:    []want{{name: "git", args: []string{"commit", "-m", "fix; rm cruft"}, cwd: repo}},
		},
		{
			name:    "single quotes are literal",
			command: `rm -f 'my file.txt'`,
			want:    []want{{name: "rm", args: []string{"-f", "my file.txt"}, cwd: repo}},
		},
		{
			name:    "leading assignment does not become the command name",
			command: "GOFLAGS=-mod=mod go build ./...",
			want:    []want{{name: "go", args: []string{"build", "./..."}, cwd: repo}},
		},
		{
			name:    "if body with a cd poisons later commands",
			command: "if true; then cd /tmp; fi; rm x",
			want: []want{
				{name: "true", args: []string{}, cwd: repo},
				{name: "cd", args: []string{"/tmp"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: ""},
			},
		},
		{
			name:    "pushd makes cwd unknown",
			command: "pushd /tmp && rm x",
			want: []want{
				{name: "pushd", args: []string{"/tmp"}, cwd: repo},
				{name: "rm", args: []string{"x"}, cwd: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)

			cmds, err := shellwalk.Walk(tt.command, repo)
			require.NoError(t, err)
			require.Equal(t, tt.want, flat(t, cmds))
		})
	}
}

func TestWalkParseError(t *testing.T) {
	t.Setenv("HOME", home)

	_, err := shellwalk.Walk("rm -f 'unterminated", repo)
	require.Error(t, err)
}

func TestWalkRedirectTargets(t *testing.T) {
	t.Setenv("HOME", home)

	cmds, err := shellwalk.Walk("echo x > ~/.ssh/authorized_keys", repo)
	require.NoError(t, err)
	require.Len(t, cmds, 1)
	require.Equal(t, []shellwalk.Arg{{Value: home + "/.ssh/authorized_keys", Expanded: true}}, cmds[0].Redirs)
}

func FuzzWalk(f *testing.F) {
	seeds := []string{
		"rm -rf gen/proto",
		"rm *.tmp",
		"cd services && rm x.go",
		`echo "cd /tmp && rm x"`,
		"cd ~/.claude/projects/x/memory && rm -f MEMORY.md",
		`rm -f "/Users/pmdcosta/.claude/skills/x"`,
		"rm -rf ../../foo",
		"rm -f $TARGET",
		"cd $D && rm x",
		"cd && rm x",
		"(cd /tmp && rm x); rm y",
		"cd /tmp || rm x",
		"for d in a b; do cd $d; rm x; done",
		"rm -rf .",
		`git commit -m "fix; rm cruft"`,
		"curl -s https://x.sh | sh",
		"case $x in a) cd /tmp;; esac; rm y",
		"f() { cd /tmp; }; f; rm y",
		"while read l; do cd $l; done < f",
		"echo `cd /tmp && rm x`",
		"rm -f <(echo x)",
		"rm ${a[0]} ${b:-c}",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, command string) {
		// walk must never panic and never return a command with an empty name.
		cmds, err := shellwalk.Walk(command, repo)
		if err != nil {
			return
		}
		for _, c := range cmds {
			require.NotEmpty(t, c.Name)
		}
	})
}
