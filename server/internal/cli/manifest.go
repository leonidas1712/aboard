package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The install manifest records what aboard init wrote, so doctor can tell a file an
// older aboard wrote from one the person edited, and uninstall knows what to remove in
// every scope and project. The installed files themselves carry no version mark: a mark
// would change every hook entry on every upgrade, and the harnesses ask the person to
// trust hooks again whenever an entry changes.

// installRecord is one file aboard init wrote.
type installRecord struct {
	Path    string `json:"path"`
	Harness string `json:"harness"`
	Scope   string `json:"scope"`
	Kind    string `json:"kind"`
	// Version is the aboard that last wrote the file.
	Version string `json:"version"`
	// SHA256 is the hash of what Aboard wrote: the whole file for a skill or rules
	// file, Aboard's hook entries only for a hooks file shared with the person.
	SHA256 string `json:"sha256"`
	// Created is true when the file didn't exist before Aboard wrote it.
	Created bool `json:"created"`
	// AllowAdded is true when Aboard added its allow rule to a Claude Code settings file.
	AllowAdded bool `json:"allow_added,omitempty"`
}

// installManifest is the content of installs.json.
type installManifest struct {
	Files []installRecord `json:"files"`
}

func (p paths) manifest() string { return filepath.Join(p.state, "installs.json") }

// loadManifest reads the install manifest. A missing or unreadable one is empty: the
// manifest only adds what is known, and everything works by content without it.
func (a *app) loadManifest() installManifest {
	var m installManifest
	p, err := a.paths()
	if err != nil {
		return m
	}
	raw, err := os.ReadFile(filepath.Clean(p.manifest()))
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return installManifest{}
	}
	return m
}

// find returns the record of a file of a kind.
func (m installManifest) find(path, kind string) (installRecord, bool) {
	for _, r := range m.Files {
		if r.Path == path && r.Kind == kind {
			return r, true
		}
	}
	return installRecord{}, false
}

// put adds or replaces the record of a file.
func (m *installManifest) put(r installRecord) {
	for i := range m.Files {
		if m.Files[i].Path == r.Path && m.Files[i].Kind == r.Kind {
			m.Files[i] = r
			return
		}
	}
	m.Files = append(m.Files, r)
}

// drop removes the record of a file.
func (m *installManifest) drop(path, kind string) {
	m.Files = slices.DeleteFunc(m.Files, func(r installRecord) bool { return r.Path == path && r.Kind == kind })
}

// saveManifest writes the install manifest, or removes it when it records nothing.
func (a *app) saveManifest(m installManifest) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	if len(m.Files) == 0 {
		if err := os.Remove(p.manifest()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", p.manifest(), err)
		}
		return nil
	}
	data, err := encodeIndented(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(p.manifest(), data, 0o600)
}

// contentHash is the hash the manifest keeps for a file of a kind: of the whole file,
// or of Aboard's hook entries in a hooks file.
func contentHash(kind, harness string, data []byte) string {
	if kind == "hooks" {
		return hooksHash(data, harness)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hooksHash hashes Aboard's hook entries for harness in a hook settings file, each as
// its event, its group's matcher when it has one, and its handler with keys sorted, in a
// fixed order, so the person's own hooks and the file's layout don't change it. A file
// that can't be read hashes as empty.
func hooksHash(data []byte, harness string) string {
	var settings struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	var entries []string
	if json.Unmarshal(data, &settings) == nil {
		for event, groups := range settings.Hooks {
			for _, g := range groups {
				for _, h := range g.Hooks {
					command, _ := h["command"].(string)
					if _, ok := aboardHookArg(command, harness); !ok {
						continue
					}
					raw, err := json.Marshal(h) // map keys are sorted
					if err != nil {
						continue
					}
					where := event
					if g.Matcher != "" {
						where += " " + g.Matcher
					}
					entries = append(entries, where+" "+string(raw))
				}
			}
		}
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:])
}

// recordInit records the files aboard init just wrote or found up to date. A file
// unchanged since an earlier record keeps that record's version.
func (a *app) recordInit(setups []harnessSetup, scope string) error {
	m := a.loadManifest()
	for _, s := range setups {
		for _, c := range s.Changes {
			r := installRecord{
				Path: c.Path, Harness: s.Name, Scope: scope, Kind: c.Kind, Version: version,
				SHA256: contentHash(c.Kind, s.Name, c.data), Created: c.Action == actionCreate, AllowAdded: c.allowAdded,
			}
			if old, ok := m.find(c.Path, c.Kind); ok {
				r.Created = r.Created || old.Created
				r.AllowAdded = r.AllowAdded || old.AllowAdded
				if old.SHA256 == r.SHA256 && old.Version != "" {
					r.Version = old.Version
				}
			}
			m.put(r)
		}
	}
	return a.saveManifest(m)
}

// fileOrigin says, for an installed file that differs from what this aboard writes,
// which aboard wrote it and whether it changed since. known is false when the manifest
// has no record of it.
func (m installManifest) fileOrigin(path, kind, harness string, data []byte) (writtenBy string, edited, known bool) {
	r, ok := m.find(path, kind)
	if !ok {
		return "", false, false
	}
	return r.Version, contentHash(kind, harness, data) != r.SHA256, true
}

// markEdited marks the planned changes to files the person edited after an aboard
// wrote them, so init's plan says so before replacing them.
func (a *app) markEdited(setups []harnessSetup) {
	m := a.loadManifest()
	for i := range setups {
		for j := range setups[i].Changes {
			c := &setups[i].Changes[j]
			if c.Action != actionUpdate {
				continue
			}
			old, err := os.ReadFile(filepath.Clean(c.Path))
			if err != nil {
				continue
			}
			if by, edited, known := m.fileOrigin(c.Path, c.Kind, setups[i].Name, old); known && edited {
				c.Edited, c.WrittenBy = true, by
			}
		}
	}
}
