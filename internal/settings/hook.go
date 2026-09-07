package settings

import (
	"encoding/json"
	"fmt"
	"strings"
)

// hooksKey is the top-level key holding every hook registration.
const hooksKey = "hooks"

// handler is one command hook entry, decoded only to read its command.
type handler struct {
	Type    string `json:"type"`
	Timeout int    `json:"timeout,omitempty"`
	Command string `json:"command"`
}

// group is one matcher group under an event, holding a list of handlers.
type group struct {
	Hooks []handler `json:"hooks"`
}

// Registered reports whether bouncer already owns a handler for the event.
func (f *File) Registered() (bool, error) {
	return f.handlerMatching(marker)
}

// RemoteApproverRegistered reports whether claude-remote-approver owns a
// handler for the event.
func (f *File) RemoteApproverRegistered() (bool, error) {
	return f.handlerMatching(remoteApprover)
}

// handlerMatching reports whether any handler's command contains needle.
func (f *File) handlerMatching(needle string) (bool, error) {
	groups, err := f.eventGroups()
	if err != nil {
		return false, err
	}

	for _, raw := range groups {
		var g group
		if err := json.Unmarshal(raw, &g); err != nil {
			// a group bouncer cannot read is not one it owns.
			continue
		}

		for _, h := range g.Hooks {
			if strings.Contains(h.Command, needle) {
				return true, nil
			}
		}
	}

	return false, nil
}

// eventGroups returns the raw matcher groups registered for the event.
func (f *File) eventGroups() ([]json.RawMessage, error) {
	hooks, err := f.doc.child(hooksKey)
	if err != nil {
		return nil, err
	}

	raw, found := hooks.get(Event)
	if !found {
		return nil, nil
	}

	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("failed to parse %s hooks: %w", Event, err)
	}

	return groups, nil
}

// Register adds bouncer's handler for the event, reporting whether the
// document changed.
//
// Running it twice is a no-op rather than a duplicate entry, because a handler
// is recognised by its command containing bouncer's name.
func (f *File) Register(command string) (bool, error) {
	present, err := f.Registered()
	if err != nil {
		return false, err
	}

	if present {
		return false, nil
	}

	groups, err := f.eventGroups()
	if err != nil {
		return false, err
	}

	entry, err := json.Marshal(group{Hooks: []handler{{
		Type:    "command",
		Timeout: Timeout,
		Command: command,
	}}})
	if err != nil {
		return false, fmt.Errorf("failed to encode hook entry: %w", err)
	}

	if err := f.setEventGroups(append(groups, entry)); err != nil {
		return false, err
	}

	return true, nil
}

// Unregister removes bouncer's handler, reporting whether the document
// changed.
//
// Every other handler and setting is preserved as its original bytes. A group
// left with no handlers is dropped, and so is the event key itself, rather
// than leaving an empty list behind.
func (f *File) Unregister() (bool, error) {
	groups, err := f.eventGroups()
	if err != nil {
		return false, err
	}

	kept := make([]json.RawMessage, 0, len(groups))
	changed := false

	for _, raw := range groups {
		trimmed, dropped, err := withoutBouncer(raw)
		if err != nil {
			return false, err
		}

		if !dropped {
			kept = append(kept, raw)
			continue
		}

		changed = true

		if trimmed != nil {
			kept = append(kept, trimmed)
		}
	}

	if !changed {
		return false, nil
	}

	if err := f.setEventGroups(kept); err != nil {
		return false, err
	}

	return true, nil
}

// withoutBouncer removes bouncer's handlers from one matcher group. It returns
// nil bytes when the group holds nothing else.
func withoutBouncer(raw json.RawMessage) (json.RawMessage, bool, error) {
	var g object
	if err := json.Unmarshal(raw, &g); err != nil {
		// not an object bouncer wrote, so leave it exactly as it is.
		return raw, false, nil
	}

	list, found := g.get("hooks")
	if !found {
		return raw, false, nil
	}

	var handlers []json.RawMessage
	if err := json.Unmarshal(list, &handlers); err != nil {
		return raw, false, nil
	}

	kept := make([]json.RawMessage, 0, len(handlers))
	dropped := false

	for _, h := range handlers {
		var decoded handler
		if err := json.Unmarshal(h, &decoded); err == nil && strings.Contains(decoded.Command, marker) {
			dropped = true
			continue
		}

		kept = append(kept, h)
	}

	if !dropped {
		return raw, false, nil
	}

	if len(kept) == 0 {
		return nil, true, nil
	}

	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, false, fmt.Errorf("failed to encode hook list: %w", err)
	}

	g.set("hooks", encoded)

	trimmed, err := json.Marshal(g)
	if err != nil {
		return nil, false, fmt.Errorf("failed to encode hook group: %w", err)
	}

	return trimmed, true, nil
}

// setEventGroups writes the event's groups back, dropping empty containers so
// no "PermissionRequest": [] is left behind.
func (f *File) setEventGroups(groups []json.RawMessage) error {
	hooks, err := f.doc.child(hooksKey)
	if err != nil {
		return fmt.Errorf("failed to update hooks: %w", err)
	}

	if len(groups) == 0 {
		hooks.del(Event)

		return f.doc.setChild(hooksKey, hooks)
	}

	encoded, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("failed to encode %s hooks: %w", Event, err)
	}

	hooks.set(Event, encoded)

	return f.doc.setChild(hooksKey, hooks)
}

// HookCommand returns the command line to register for a binary path.
//
// The path must be absolute: hooks do not inherit an interactive shell's PATH,
// so a bare "bouncer" is not found.
func HookCommand(binary string) string {
	return binary + " decide"
}
