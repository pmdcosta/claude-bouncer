// Package hook implements the Claude Code PermissionRequest stdin and stdout
// contract, and answers one request against a rule list.
//
// The contract is easy to get wrong in a way that fails silently: the output
// field is decision.behavior, not the flat permissionDecision that PreToolUse
// uses. A wrong shape produces no error at all, the hook simply does nothing,
// so the exact bytes are pinned by a golden test.
package hook

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/pmdcosta/claude-bouncer/internal/shellwalk"
)

// EventName is the only hook event bouncer answers.
const EventName = "PermissionRequest"

// Decision is what bouncer tells Claude Code to do.
//
// The zero value is Ask, so any path that forgets to set a decision falls back
// to the normal permission prompt.
type Decision int

// The two decisions bouncer emits. It never denies anything.
const (
	// Ask leaves the permission flow unchanged, which shows the normal
	// prompt.
	Ask Decision = iota
	// Allow grants the permission without a prompt.
	Allow
)

// String renders the decision for the audit log.
func (d Decision) String() string {
	if d == Allow {
		return "allow"
	}

	return "ask"
}

// Request is the hook's stdin payload, reduced to the fields bouncer reads.
type Request struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	EventName string `json:"hook_event_name"`
	ToolName  string `json:"tool_name"`
	// ToolInput is kept raw because its shape varies per tool, and an MCP tool
	// with an unexpected shape must not fail the whole decode.
	ToolInput json.RawMessage `json:"tool_input"`
}

// Input holds the only two tool_input fields any rule reads.
type Input struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
}

// Input decodes the tool input, returning the zero value for any shape it does
// not recognise.
func (r Request) Input() Input {
	var in Input
	if err := json.Unmarshal(r.ToolInput, &in); err != nil {
		return Input{}
	}

	return in
}

// Read decodes a request.
func Read(r io.Reader) (Request, error) {
	var req Request

	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return Request{}, fmt.Errorf("failed to decode hook request: %w", err)
	}

	if req.EventName != "" && req.EventName != EventName {
		return Request{}, fmt.Errorf("failed to handle event %q: not a %s", req.EventName, EventName)
	}

	return req, nil
}

// output mirrors the documented stdout shape. An Ask carries no decision
// object at all, which is how a hook leaves the permission flow unchanged.
type output struct {
	HookSpecificOutput specific `json:"hookSpecificOutput"`
}

type specific struct {
	HookEventName string    `json:"hookEventName"`
	Decision      *decision `json:"decision,omitempty"`
}

type decision struct {
	Behavior string `json:"behavior"`
}

// Write encodes the decision.
func Write(w io.Writer, d Decision) error {
	out := output{HookSpecificOutput: specific{HookEventName: EventName}}
	if d == Allow {
		out.HookSpecificOutput.Decision = &decision{Behavior: "allow"}
	}

	if err := json.NewEncoder(w).Encode(out); err != nil {
		return fmt.Errorf("failed to encode hook decision: %w", err)
	}

	return nil
}

// Handler answers permission requests against a rule list.
type Handler struct {
	Rules []rules.Rule
}

// Decide answers one request, returning the decision and the name of the rule
// that required a prompt.
//
// A returned error means the command could not be understood. The decision is
// still valid and is always Ask in that case: bouncer never allows on an error
// path.
func (h Handler) Decide(req Request) (Decision, string, error) {
	in := req.Input()

	var cmds []shellwalk.Command
	if in.Command != "" {
		walked, err := shellwalk.Walk(in.Command, req.Cwd)
		if err != nil {
			return Ask, "", fmt.Errorf("failed to walk command: %w", err)
		}

		cmds = walked
	}

	name := rules.Match(h.Rules, rules.Request{
		ToolName: req.ToolName,
		FilePath: in.FilePath,
		Commands: cmds,
	})

	if name != "" {
		return Ask, name, nil
	}

	return Allow, "", nil
}
