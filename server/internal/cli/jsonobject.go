package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// jsonObject is a JSON object that keeps its keys in order and its values as written,
// so editing one key of someone's settings file leaves everything else as it was.
type jsonObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func newJSONObject() *jsonObject { return &jsonObject{vals: map[string]json.RawMessage{}} }

// parseJSONObject reads a JSON object. Empty input is an empty object.
func parseJSONObject(data []byte) (*jsonObject, error) {
	o := newJSONObject()
	if len(bytes.TrimSpace(data)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("read JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("read JSON: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("read JSON value of %q: %w", key, err)
		}
		o.set(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("read JSON: %w", err)
	}
	return o, nil
}

func (o *jsonObject) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *jsonObject) set(key string, v json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *jsonObject) remove(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// MarshalJSON writes the keys in their order.
func (o *jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("encode key %q: %w", k, err)
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// encodeIndented writes v as indented JSON with a final newline, without escaping
// "<", ">" and "&".
func encodeIndented(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(buf.Bytes()), "", "  "); err != nil {
		return nil, fmt.Errorf("indent JSON: %w", err)
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
