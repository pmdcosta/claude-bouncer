package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// object is a JSON object that remembers the order its keys were read in, and
// keeps every value as the raw bytes it was read as.
//
// This is why settings.json survives an edit intact: a plain map would decode
// into a struct-shaped subset and reorder every key on write, churning parts
// of the file bouncer has no business touching.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

// UnmarshalJSON records the key order alongside the values.
func (o *object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))

	open, err := dec.Token()
	if err != nil {
		return fmt.Errorf("failed to read object: %w", err)
	}

	if open != json.Delim('{') {
		return fmt.Errorf("failed to read object: got %v", open)
	}

	o.keys = nil
	o.vals = map[string]json.RawMessage{}

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return fmt.Errorf("failed to read object key: %w", err)
		}

		key, isString := token.(string)
		if !isString {
			return fmt.Errorf("failed to read object key: got %v", token)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("failed to read value of %q: %w", key, err)
		}

		o.set(key, raw)
	}

	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("failed to close object: %w", err)
	}

	return nil
}

// MarshalJSON writes the keys back in their original order. The encoder that
// consumes this re-indents the result, so the raw values need no formatting.
func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer

	b.WriteByte('{')

	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}

		name, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("failed to encode key %q: %w", key, err)
		}

		b.Write(name)
		b.WriteByte(':')
		b.Write(o.vals[key])
	}

	b.WriteByte('}')

	return b.Bytes(), nil
}

// get returns a value.
func (o object) get(key string) (json.RawMessage, bool) {
	raw, found := o.vals[key]

	return raw, found
}

// set stores a value, appending the key if it is new.
func (o *object) set(key string, raw json.RawMessage) {
	if o.vals == nil {
		o.vals = map[string]json.RawMessage{}
	}

	if _, found := o.vals[key]; !found {
		o.keys = append(o.keys, key)
	}

	o.vals[key] = raw
}

// del removes a key.
func (o *object) del(key string) {
	if _, found := o.vals[key]; !found {
		return
	}

	delete(o.vals, key)

	kept := make([]string, 0, len(o.keys)-1)
	for _, k := range o.keys {
		if k != key {
			kept = append(kept, k)
		}
	}

	o.keys = kept
}

// empty reports whether the object holds no keys.
func (o object) empty() bool {
	return len(o.keys) == 0
}

// child decodes a nested object, returning an empty one when the key is
// absent.
func (o object) child(key string) (object, error) {
	raw, found := o.get(key)
	if !found {
		return object{}, nil
	}

	var nested object
	if err := json.Unmarshal(raw, &nested); err != nil {
		return object{}, fmt.Errorf("failed to parse %q: %w", key, err)
	}

	return nested, nil
}

// setChild stores a nested object, or removes the key when it is empty.
func (o *object) setChild(key string, nested object) error {
	if nested.empty() {
		o.del(key)

		return nil
	}

	raw, err := json.Marshal(nested)
	if err != nil {
		return fmt.Errorf("failed to encode %q: %w", key, err)
	}

	o.set(key, raw)

	return nil
}
