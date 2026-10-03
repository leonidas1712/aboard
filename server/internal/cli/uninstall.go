package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	skill "github.com/leonidas1712/aboard/skills/aboard"
)

// Uninstall actions for one file.
const (
	actionDelete = "delete"
	actionEdit   = "edit"
	actionKeep   = "keep"
)

// reasonEdited is why uninstall keeps a file: it changed after an aboard wrote it.
const reasonEdited = "edited"

// uninstallFile is one file aboard uninstall deletes, edits or keeps.
type uninstallFile struct {
	Path      string  `json:"path"`
	Harness   string  `json:"harness"`
	Scope     string  `json:"scope"`
	Kind      string  `json:"kind"`
	Action    string  `json:"action"`
	Reason    *string `json:"reason"`
	WrittenBy *string `json:"written_by"`
	// data is the file's new content, for an edit.
	data []byte
	perm os.FileMode
	// note says more in text output.
	note string
}

// uninstallData is what aboard uninstall does with Aboard's own folders.
type uninstallData struct {
	Delete  bool     `json:"delete"`
	Deleted bool     `json:"deleted"`
	Dirs    []string `json:"dirs"`
}

// uninstallBinary is where the aboard binary is, and how to remove it.
type uninstallBinary struct {
	Path   string `json:"path"`
	Remove string `json:"remove"`
}

// runUninstall stops the local server and the delivery daemon, removes what aboard init
// wrote into harness settings, and with --data deletes Aboard's folders. It leaves the
// binary, which it can't tell how to remove reliably, and says how.
func runUninstall(ctx context.Context, a *app, args []string) error {
	const use = "aboard uninstall [--data] [--dry-run] [--yes] [--json]"
	flags := a.flags("uninstall")
	withData := flags.Bool("data", false, "also delete Aboard's data: boards, messages, logins and the delivery journal")
	dryRun := flags.Bool("dry-run", false, "list what would be removed, and change nothing")
	yes := flags.Bool("yes", false, "delete the data without asking")
	if _, err := a.parse(flags, args, use, 0, 0); err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	again := "aboard uninstall --data"
	if *withData && !*dryRun {
		if err := a.refuseInSession("Deleting Aboard's data", again); err != nil {
			return err
		}
	}
	m := a.loadManifest()
	files, err := a.planUninstall(m)
	if err != nil {
		return err
	}
	data := uninstallData{Delete: *withData, Dirs: existingDirs(p)}
	home := a.env.Getenv("HOME")
	where := dataWhere(p, data.Dirs, home)

	if *withData && !*dryRun && !*yes && len(data.Dirs) > 0 {
		if !a.env.Terminal || a.json {
			return newError("confirmation_required",
				"aboard uninstall --data deletes every board, message and login in "+where+", and needs a yes first.",
				"Run "+again+" --yes to delete it, or "+again+" --dry-run to see everything it removes.")
		}
		pr := &prompter{in: bufio.NewReader(a.env.Stdin), out: a.env.Stdout}
		if !pr.yes("Delete Aboard's data in " + where + ": every board, message and login on this machine?") {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
	}

	srv := a.localServer()
	var serverStopped, daemonStopped bool
	if *dryRun {
		serverStopped = a.localRunning(ctx)
		st, _ := a.daemonStatus(ctx)
		daemonStopped = st != nil
	} else {
		if serverStopped, daemonStopped, err = a.stopAll(ctx); err != nil {
			return err
		}
		if err := a.applyUninstall(files, &m); err != nil {
			return err
		}
		if *withData {
			if err := deleteData(p); err != nil {
				return err
			}
			data.Deleted = true
		}
	}

	var text strings.Builder
	if *dryRun {
		text.WriteString(wouldStopText(srv, serverStopped, daemonStopped))
	} else {
		text.WriteString(stoppedText(srv, serverStopped, daemonStopped))
	}
	if len(files) == 0 {
		text.WriteString("No Aboard files found in harness settings.\n")
	}
	for _, f := range files {
		fmt.Fprintf(&text, "%-8s %s (%s %s%s)\n", f.Action, shortPath(f.Path, home), f.Harness, f.Kind, f.note)
	}
	switch {
	case len(data.Dirs) == 0:
		text.WriteString("Aboard has no data on this machine.\n")
	case !*withData:
		text.WriteString("Kept Aboard's data in " + where + "; aboard uninstall --data deletes it.\n")
	case *dryRun:
		text.WriteString("Would delete Aboard's data in " + where + ".\n")
	default:
		text.WriteString("Deleted Aboard's data in " + where + ".\n")
	}
	bin := a.binaryRemoval()
	text.WriteString("aboard uninstall leaves the binary at " + bin.Path + ". Remove it with: " + bin.Remove + "\n")
	if *dryRun {
		cmd := "aboard uninstall"
		if *withData {
			cmd += " --data"
		}
		text.WriteString("Run " + cmd + " to make these changes.\n")
	}
	a.emit(struct {
		Applied       bool            `json:"applied"`
		Server        serverRef       `json:"server"`
		ServerStopped bool            `json:"server_stopped"`
		DaemonStopped bool            `json:"daemon_stopped"`
		Files         []uninstallFile `json:"files"`
		Data          uninstallData   `json:"data"`
		Binary        uninstallBinary `json:"binary"`
	}{!*dryRun, srv, serverStopped, daemonStopped, files, data, bin}, text.String())
	return nil
}

// uninstallKinds orders a harness's files in uninstall's output.
var uninstallKinds = []string{"skill", "hooks", "permissions"}

// planUninstall works out what to do with each Aboard file: those the install manifest
// records, in any scope and project, and, for installs from before the manifest, those
// found by content in both scopes from the working directory. It changes nothing.
func (a *app) planUninstall(m installManifest) ([]uninstallFile, error) {
	type candidate struct {
		path, harness, scope, kind string
		rec                        *installRecord
	}
	var cands []candidate
	for i := range m.Files {
		r := &m.Files[i]
		cands = append(cands, candidate{r.Path, r.Harness, r.Scope, r.Kind, r})
	}
	known := func(path, kind string) bool {
		_, ok := m.find(path, kind)
		return ok
	}
	for _, harness := range []string{"claude-code", "codex"} {
		for _, scope := range a.scopes() {
			f := a.setupFiles(harness, scope)
			if !known(f.skill, "skill") {
				cands = append(cands, candidate{f.skill, harness, scope, "skill", nil})
			}
			if !known(f.hooks, "hooks") {
				cands = append(cands, candidate{f.hooks, harness, scope, "hooks", nil})
			}
			if harness == "codex" && !known(f.allow, "permissions") {
				cands = append(cands, candidate{f.allow, harness, scope, "permissions", nil})
			}
		}
	}
	files := []uninstallFile{}
	for _, c := range cands {
		data, err := os.ReadFile(filepath.Clean(c.path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", c.path, err)
		}
		f := uninstallFile{Path: c.path, Harness: c.harness, Scope: c.scope, Kind: c.kind}
		if info, err := os.Stat(c.path); err == nil {
			f.perm = info.Mode().Perm()
		}
		if c.rec != nil {
			by := c.rec.Version
			f.WrittenBy = &by
		}
		switch c.kind {
		case "hooks":
			removeAllow := c.harness == "claude-code" && (c.rec == nil || c.rec.AllowAdded)
			out, changed, empty, err := removeAboardEntries(data, c.harness, removeAllow)
			if err != nil {
				return nil, &Error{
					Code: "invalid_request", Message: "Couldn't read the hook settings in " + c.path + ": " + err.Error(),
					Hint: "Fix the file so it is a JSON object, then run aboard uninstall again.", Err: err,
				}
			}
			switch {
			case !changed:
				continue // nothing of Aboard's is in it
			case empty:
				f.Action, f.note = actionDelete, ": it held only Aboard's entries"
			default:
				f.Action, f.data, f.note = actionEdit, out, ": took out Aboard's entries, kept the rest"
			}
		default:
			want := skill.Skill
			if c.kind == "permissions" {
				want = codexRules
			}
			unchanged := bytes.Equal(data, want)
			if c.rec != nil {
				unchanged = contentHash(c.kind, c.harness, data) == c.rec.SHA256
			}
			f.Action = actionDelete
			if !unchanged {
				reason := reasonEdited
				f.Action, f.Reason = actionKeep, &reason
				if c.rec != nil {
					f.note = ": edited since aboard " + c.rec.Version + " wrote it; remove it yourself if you don't need it"
				} else {
					f.note = ": differs from the one aboard " + version + " installs; remove it yourself if you don't need it"
				}
			}
		}
		files = append(files, f)
	}
	slices.SortStableFunc(files, func(x, y uninstallFile) int {
		if c := strings.Compare(x.Harness, y.Harness); c != 0 {
			return c
		}
		if x.Scope != y.Scope {
			if x.Scope == scopeGlobal {
				return -1
			}
			return 1
		}
		if c := slices.Index(uninstallKinds, x.Kind) - slices.Index(uninstallKinds, y.Kind); c != 0 {
			return c
		}
		return strings.Compare(x.Path, y.Path)
	})
	return files, nil
}

// applyUninstall deletes and edits the planned files, and keeps the manifest's records
// only of the files it kept.
func (a *app) applyUninstall(files []uninstallFile, m *installManifest) error {
	for _, f := range files {
		switch f.Action {
		case actionDelete:
			if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", f.Path, err)
			}
			if f.Kind == "skill" {
				_ = os.Remove(filepath.Dir(f.Path)) // the skill's folder, once empty
			}
		case actionEdit:
			if err := writeFileAtomic(f.Path, f.data, f.perm); err != nil {
				return err
			}
		default:
			continue
		}
		m.drop(f.Path, f.Kind)
	}
	// Records of files that are gone are no use to anyone.
	m.Files = slices.DeleteFunc(m.Files, func(r installRecord) bool {
		_, err := os.Stat(r.Path)
		return errors.Is(err, fs.ErrNotExist)
	})
	return a.saveManifest(*m)
}

// removeAboardEntries takes Aboard's hook entries for harness out of a hook settings
// file, and with allow Claude Code's allow rule for aboard, keeping everything else as
// it was. Objects and lists left empty by that go too. empty is true when nothing is
// left in the file.
func removeAboardEntries(data []byte, harness string, allow bool) (out []byte, changed, empty bool, err error) {
	root, err := parseJSONObject(data)
	if err != nil {
		return nil, false, false, err
	}
	if raw, ok := root.get("hooks"); ok && string(bytes.TrimSpace(raw)) != "null" {
		hooks, err := parseJSONObject(raw)
		if err != nil {
			return nil, false, false, fmt.Errorf("hooks: %w", err)
		}
		removed, err := removeStaleHooks(hooks, harness, nil)
		if err != nil {
			return nil, false, false, err
		}
		if removed {
			changed = true
			if len(hooks.keys) == 0 {
				root.remove("hooks")
			} else if err := setJSON(root, "hooks", hooks); err != nil {
				return nil, false, false, err
			}
		}
	}
	if allow {
		removed, err := removeClaudeAllow(root)
		if err != nil {
			return nil, false, false, err
		}
		changed = changed || removed
	}
	if !changed {
		return data, false, false, nil
	}
	out, err = encodeIndented(root)
	return out, true, len(root.keys) == 0, err
}

// removeClaudeAllow takes Aboard's allow rule out of permissions.allow in Claude Code
// settings, removing the list and the permissions object if that leaves them empty.
func removeClaudeAllow(root *jsonObject) (bool, error) {
	raw, ok := root.get("permissions")
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return false, nil
	}
	perms, err := parseJSONObject(raw)
	if err != nil {
		return false, fmt.Errorf("permissions: %w", err)
	}
	rawAllow, ok := perms.get("allow")
	if !ok {
		return false, nil
	}
	var rules []json.RawMessage
	if err := json.Unmarshal(rawAllow, &rules); err != nil {
		return false, fmt.Errorf("permissions.allow: %w", err)
	}
	kept := slices.DeleteFunc(slices.Clone(rules), func(r json.RawMessage) bool {
		var s string
		return json.Unmarshal(r, &s) == nil && s == claudeAllowRule
	})
	if len(kept) == len(rules) {
		return false, nil
	}
	if len(kept) == 0 {
		perms.remove("allow")
	} else if err := setJSON(perms, "allow", kept); err != nil {
		return false, err
	}
	if len(perms.keys) == 0 {
		root.remove("permissions")
		return true, nil
	}
	return true, setJSON(root, "permissions", perms)
}

// setJSON sets key in o to v encoded as JSON.
func setJSON(o *jsonObject, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	o.set(key, raw)
	return nil
}

// existingDirs returns Aboard's config, data and state folders that exist.
func existingDirs(p paths) []string {
	dirs := []string{}
	for _, d := range []string{p.config, p.data, p.state} {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// dataWhere names where Aboard's data is, for text output: ABOARD_HOME when it is
// set, else each folder.
func dataWhere(p paths, dirs []string, home string) string {
	if p.home != "" {
		return shortPath(p.home, home)
	}
	short := make([]string, len(dirs))
	for i, d := range dirs {
		short[i] = shortPath(d, home)
	}
	return strings.Join(short, ", ")
}

// deleteData deletes Aboard's config, data and state folders, and ABOARD_HOME once it
// is empty. Only the folders Aboard named for itself are removed, so an ABOARD_HOME
// that holds anything else stays.
func deleteData(p paths) error {
	for _, d := range []string{p.config, p.data, p.state} {
		if err := os.RemoveAll(d); err != nil {
			return fmt.Errorf("remove %s: %w", d, err)
		}
	}
	if p.home != "" {
		_ = os.Remove(p.home) // only once empty
	}
	return nil
}

// binaryRemoval returns the running binary's path, links resolved, and the command
// that removes it.
func (a *app) binaryRemoval() uninstallBinary {
	exe := a.hookExe()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.Contains(exe, "/Cellar/") {
		return uninstallBinary{Path: exe, Remove: "brew uninstall aboard"}
	}
	return uninstallBinary{Path: exe, Remove: "rm " + shellWord(exe)}
}

// wouldStopText says what aboard uninstall --dry-run would stop.
func wouldStopText(srv serverRef, server, daemon bool) string {
	switch {
	case server && daemon:
		return "Would stop local Aboard at " + srv.URL + " and the delivery daemon.\n"
	case server:
		return "Would stop local Aboard at " + srv.URL + ". The delivery daemon isn't running.\n"
	case daemon:
		return "Would stop the delivery daemon. Local Aboard isn't running.\n"
	}
	return "Local Aboard and the delivery daemon aren't running.\n"
}

// shortPath writes a path under the home directory as ~/….
func shortPath(p, home string) string {
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + rel
	}
	return p
}
