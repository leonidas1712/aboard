package delivery

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	// specJSONBlock is a fenced JSON block in spec/control.md, possibly indented in a list.
	specJSONBlock = regexp.MustCompile("(?ms)^[ \t]*```json\n(.*?)^[ \t]*```")
	// specInlineFrame is a whole message written inline, as in the extension's table.
	specInlineFrame = regexp.MustCompile("`(\\{\"v\":[^`]*\\})`")
)

// Every message spec/control.md shows decodes into the daemon's own Request or Response
// with no field left over, and writes back the same, so the page names exactly the fields
// the daemon reads and writes, and leaves out empty ones as the daemon does. Every
// operation and event the code declares is named on the page.
func TestControlSpecExamplesMatchTheProtocol(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/control.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	var frames []string
	for _, m := range specJSONBlock.FindAllStringSubmatch(doc, -1) {
		for _, line := range strings.Split(m[1], "\n") {
			if line = strings.TrimSpace(line); line != "" {
				frames = append(frames, line)
			}
		}
	}
	for _, m := range specInlineFrame.FindAllStringSubmatch(doc, -1) {
		frames = append(frames, m[1])
	}
	if len(frames) < 20 {
		t.Fatalf("found %d example messages in spec/control.md; the examples moved or the pattern no longer finds them", len(frames))
	}
	for _, frame := range frames {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(frame), &probe); err != nil {
			t.Errorf("not one JSON object: %v\n%s", err, frame)
			continue
		}
		var v any = &Response{}
		if _, ok := probe["op"]; ok {
			v = &Request{}
		}
		dec := json.NewDecoder(strings.NewReader(frame))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			t.Errorf("doesn't match the daemon's %T: %v\n%s", v, err, frame)
			continue
		}
		var again bytes.Buffer
		enc := json.NewEncoder(&again)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		var want, got any
		_ = json.Unmarshal([]byte(frame), &want)
		_ = json.Unmarshal(again.Bytes(), &got)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("the daemon would write this message differently:\nspec:   %s\ndaemon: %s", frame, strings.TrimSpace(again.String()))
		}
	}
	for name, value := range protocolNames(t) {
		if !strings.Contains(doc, "`"+value+"`") && !strings.Contains(doc, `"`+value+`"`) {
			t.Errorf("%s (%q) isn't named in spec/control.md", name, value)
		}
	}
}

// protocolNames returns the operations and events protocol.go declares, by constant name.
func protocolNames(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Op") && !strings.HasPrefix(name.Name, "Event") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out[name.Name] = value
			}
		}
	}
	if len(out) < 10 {
		t.Fatalf("found %d operations and events in protocol.go", len(out))
	}
	return out
}
