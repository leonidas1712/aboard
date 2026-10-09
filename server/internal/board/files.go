package board

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// FileVersion records immutable bytes and the member who wrote them.
type FileVersion struct {
	Version, Base       int
	Digest              string
	Size                int64
	MediaType, ByID, At string
	Seq                 int64
	By                  Member
}

// SweepBlobs holds the write lock so a reused old blob cannot lose a concurrent reference.
func (s *Service) SweepBlobs(ctx context.Context) (int, error) {
	removed := 0
	if s.cfg.Blobs == nil {
		return 0, nil
	}
	err := s.st.Write(ctx, func(tx Tx) error {
		versions, err := tx.BlobVersions()
		if err != nil {
			return err
		}
		referenced := map[string]bool{}
		for _, v := range versions {
			referenced[v.Digest] = true
		}
		cutoff := s.clk.Now().Add(-24 * time.Hour)
		return s.cfg.Blobs.Walk(ctx, func(blob Blob) error {
			if referenced[blob.Digest] || !blob.Modified.Before(cutoff) {
				return nil
			}
			if err := s.cfg.Blobs.Delete(ctx, blob.Digest); err != nil {
				return err
			}
			removed++
			return nil
		})
	})
	return removed, err
}

// File retains one board path and every version written to it.
type File struct {
	PostedIn                                    []FilePostedIn
	MessagesSince, TasksDoneSince, AnswersSince int

	ID, BoardID, Board, Name string
	Maintained               bool
	Removed                  bool
	About                    []TaskRef
	Versions                 []FileVersion
}

// FileSelector chooses a board-local file version at message commit time.
type FileSelector struct {
	File    string `json:"file"`
	Version *int   `json:"version,omitempty"`
}

// FileRef pins immutable bytes in the message record.
type FileRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// FilePostedIn identifies a visible message that attached a version.
type FilePostedIn struct {
	MessageID     string `json:"message_id"`
	Seq           int64  `json:"seq"`
	ThreadRootSeq *int64 `json:"thread_root_seq"`
	Version       int    `json:"version"`
}

func resolveFileRefs(tx ReadTx, b Board, selectors []FileSelector) ([]FileRef, error) {
	refs := []FileRef{}
	for _, selector := range selectors {
		f, err := tx.FileBySelector(b.ID, selector.File)
		if errors.Is(err, ErrNotFound) {
			return nil, fileError(404, "file_not_found", "The attachment is not on this board.")
		}
		if err != nil {
			return nil, err
		}
		v := f.Versions[len(f.Versions)-1]
		if selector.Version != nil {
			found := false
			for _, candidate := range f.Versions {
				if candidate.Version == *selector.Version {
					v, found = candidate, true
					break
				}
			}
			if !found {
				return nil, fileError(404, "version_not_found", "That attachment version does not exist.")
			}
		}
		refs = append(refs, FileRef{ID: f.ID, Name: f.Name, Version: v.Version, Digest: v.Digest})
	}
	return refs, nil
}

// NewFile carries upload bytes and the version the caller intends to replace.
type NewFile struct {
	Brief, ReplaceFormat bool
	FileID               *string
	Name                 string
	Base                 int
	Maintained           *bool
	About                *[]string
	MediaType            string
	Body                 io.Reader
}

func fileError(status int, code, text string) error {
	return apierr.New(status, code, text, "Read the file and its latest version before writing again.")
}

var fileCredential = regexp.MustCompile(`(?m)(?:ab[ahbkdic]_[A-Za-z0-9_-]{24,}|sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----)`)

func validFileName(name string) bool {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`).MatchString(name) || name == "" || len(name) > 200 || !utf8.ValidString(name) || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}

// PutFile checks authority and the base, then records a durable blob in one write.
func (s *Service) PutFile(ctx context.Context, p Principal, name string, in NewFile) (File, error) {
	var out File
	if !validFileName(in.Name) {
		return out, fileError(400, "invalid_request", "Use a relative file path without traversal.")
	}
	if isBriefName(in.Name) && !in.Brief {
		return out, fileError(409, "brief_path_reserved", "The brief path is reserved.")
	}
	if s.cfg.Blobs == nil {
		return out, fileError(501, "not_implemented", "This server has no blob store.")
	}
	if (in.Brief && !isBriefName(in.Name)) || (in.ReplaceFormat && !in.Brief) {
		return out, fileError(400, "invalid_request", "Brief writes need brief.md or brief.html.")
	}
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, name)
		if err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		if me.Kind == "agent" && (me.Role == nil || !b.Roles[*me.Role].Has(rules.UploadFiles)) {
			return fileError(403, "forbidden", "Your role cannot upload files.")
		}
		var replaced *File
		if in.Brief {
			active, err := activeBrief(tx, b)
			if err != nil {
				return err
			}
			if active != nil {
				if active.Name != in.Name && !in.ReplaceFormat {
					return apierr.New(409, "brief_exists", "The board already has a brief in another format.", "Get the brief, then put it with --replace-format.")
				}
				latest := active.Versions[len(active.Versions)-1]
				if in.FileID == nil || *in.FileID != active.ID || in.Base != latest.Version {
					e := apierr.New(409, "file_changed", "The brief changed since you fetched it.", "Get the latest brief before writing again.")
					by, err := tx.MemberByID(latest.ByID)
					if err != nil {
						return err
					}
					e.Details = map[string]any{"version": latest.Version, "by": map[string]any{"name": by.Name, "kind": by.Kind, "owner": by.Owner, "role": by.Role}, "at": latest.At}
					return e
				}
				if active.Name != in.Name {
					replaced = active
				}
			} else if in.FileID != nil || in.Base != 0 {
				return fileError(409, "file_changed", "The brief was removed or replaced since you fetched it.")
			}
		}
		out, err = tx.FileByName(b.ID, in.Name)
		fresh := errors.Is(err, ErrNotFound)
		if err != nil && !fresh {
			return err
		}
		if replaced == nil && in.FileID != nil && (fresh || out.ID != *in.FileID) {
			return fileError(409, "file_changed", "The file was removed or replaced since you fetched it.")
		}
		latest := 0
		if !fresh {
			latest = out.Versions[len(out.Versions)-1].Version
		}
		if replaced == nil && latest != in.Base {
			code := "file_changed"
			if in.Base == 0 {
				code = "file_exists"
			}
			e := apierr.New(409, code, "The file already has another version.", "Get the latest version, then upload with that base.")
			if latest > 0 {
				v := out.Versions[len(out.Versions)-1]
				by, err := tx.MemberByID(v.ByID)
				if err != nil {
					return err
				}
				e.Details = map[string]any{"version": latest, "by": map[string]any{"name": by.Name, "kind": by.Kind, "owner": by.Owner, "role": by.Role}, "at": v.At}
			}
			return e
		}
		now := s.clk.Now()
		if fresh {
			id, err := s.gen.ID("fil", now)
			if err != nil {
				return err
			}
			out = File{ID: id, BoardID: b.ID, Board: b.Name, Name: in.Name, About: []TaskRef{}, Versions: []FileVersion{}}
		}
		if in.Maintained != nil {
			out.Maintained = *in.Maintained
		}
		if in.About != nil {
			out.About = []TaskRef{}
			for _, sel := range *in.About {
				t, err := findTask(tx, b, sel)
				if err != nil {
					return err
				}
				out.About = append(out.About, taskRef(t))
			}
		}
		if in.Brief {
			out.Maintained = true
		}
		media := in.MediaType
		if media == "" {
			media = mime.TypeByExtension(path.Ext(in.Name))
		}
		if media == "" {
			media = "application/octet-stream"
		}
		body, err := io.ReadAll(io.LimitReader(in.Body, 50*1024*1024+1))
		if err != nil {
			return err
		}
		if len(body) > 50*1024*1024 {
			return fileError(413, "file_too_large", "Files can be at most 50 MB.")
		}
		if fileCredential.Match(body) {
			return fileError(422, "file_has_secret", "The text file contains a credential.")
		}
		if _, _, err := mime.ParseMediaType(media); err != nil {
			return fileError(400, "invalid_request", "Use a valid media type.")
		}
		blob, err := s.cfg.Blobs.Put(ctx, bytes.NewReader(body), 50*1024*1024)
		if err != nil {
			return err
		}
		ids := []string{}
		for _, t := range out.About {
			ids = append(ids, t.ID)
		}
		base := in.Base
		if replaced != nil {
			if _, err := s.append(tx, &b, "file.removed", actorOf(me), now, map[string]any{"file_id": replaced.ID, "name": replaced.Name}); err != nil {
				return err
			}
			replaced.Removed = true
			if err := tx.SaveFile(*replaced); err != nil {
				return err
			}
			base = 0
		}
		e, err := s.append(tx, &b, "file.version_added", actorOf(me), now, map[string]any{"file_id": out.ID, "name": out.Name, "version": latest + 1, "digest": blob.Digest, "size": blob.Size, "media_type": media, "base_version": base, "maintained": out.Maintained, "about": ids})
		if err != nil {
			return err
		}
		out.Versions = append(out.Versions, FileVersion{Version: latest + 1, Base: base, Digest: blob.Digest, Size: blob.Size, MediaType: media, ByID: me.ID, By: me, At: e.At, Seq: e.Seq})
		return tx.SaveFile(out)
	})
	if err == nil {
		s.notify.Changed(out.BoardID)
	}
	return out, err
}

// GetFile returns history only after rechecking current board membership.
func (s *Service) GetFile(ctx context.Context, p Principal, name, selector string) (File, error) {
	var out File
	err := s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		b, me, err := s.access(tx, p, name)
		if err != nil {
			return err
		}
		out, err = tx.FileBySelector(b.ID, selector)
		if errors.Is(err, ErrNotFound) {
			return fileError(404, "file_not_found", "The file is not on this board.")
		}
		if err != nil {
			return err
		}
		return projectFile(tx, b, me, &out)
	})
	return out, err
}

func projectFile(tx ReadTx, b Board, me Member, f *File) error {
	f.PostedIn = []FilePostedIn{}
	position := int64(0)
	for {
		messages, err := tx.Timeline(b.ID, me, readsAll(b, me), TimelineQuery{After: position, Limit: 200})
		if err != nil {
			return err
		}
		for _, msg := range messages {
			position = msg.Seq
			for _, ref := range msg.Files {
				if ref.ID == f.ID {
					f.PostedIn = append(f.PostedIn, FilePostedIn{MessageID: msg.ID, Seq: msg.Seq, ThreadRootSeq: msg.ThreadRootSeq, Version: ref.Version})
				}
			}
		}
		if len(messages) < 200 {
			break
		}
	}
	for i := range f.Versions {
		m, err := tx.MemberByID(f.Versions[i].ByID)
		if err != nil {
			return err
		}
		f.Versions[i].By = m
	}
	latest := f.Versions[len(f.Versions)-1].Seq
	f.MessagesSince, f.AnswersSince = 0, 0
	cursor := latest
	for {
		messages, err := tx.Timeline(b.ID, me, readsAll(b, me), TimelineQuery{After: cursor, Limit: 200})
		if err != nil {
			return err
		}
		f.MessagesSince += len(messages)
		for _, m := range messages {
			cursor = m.Seq
			if m.Answer != nil {
				f.AnswersSince++
			}
		}
		if len(messages) < 200 {
			break
		}
	}
	f.TasksDoneSince = 0
	after := latest
	for {
		es, err := tx.Events(b.ID, after, 200)
		if err != nil {
			return err
		}
		for _, e := range es {
			after = e.Seq
			if e.Type == "task.done" {
				f.TasksDoneSince++
			}
		}
		if len(es) < 200 {
			break
		}
	}
	return nil
}

// FileFilter narrows a member-visible listing without changing read positions.
type FileFilter struct {
	Task  string
	Mine  bool
	Limit int
}

// FileChange changes the live path or metadata without replacing any bytes.
type FileChange struct {
	Name       *string
	Maintained *bool
	About      *[]string
}

// ChangeFile and removal retain historical versions under the immutable file id.
func (s *Service) ChangeFile(ctx context.Context, p Principal, name, selector string, in FileChange, remove bool) (File, error) {
	var out File
	if in.Name != nil {
		if !validFileName(*in.Name) {
			return out, fileError(400, "invalid_request", "Use a relative file path without traversal.")
		}
		if *in.Name == "brief.md" || *in.Name == "brief.html" {
			return out, fileError(409, "brief_path_reserved", "The brief path is reserved.")
		}
	}
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, name)
		if err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		if me.Kind == "agent" && (me.Role == nil || !b.Roles[*me.Role].Has(rules.UploadFiles)) {
			return fileError(403, "forbidden", "Your role cannot change files.")
		}
		out, err = tx.FileBySelector(b.ID, selector)
		if errors.Is(err, ErrNotFound) || out.Removed {
			return fileError(404, "file_not_found", "The file is not on this board.")
		}
		if err != nil {
			return err
		}
		now := s.clk.Now()
		if remove {
			if _, err := s.append(tx, &b, "file.removed", actorOf(me), now, map[string]any{"file_id": out.ID, "name": out.Name}); err != nil {
				return err
			}
			out.Removed = true
		} else {
			if in.Name != nil && *in.Name != out.Name {
				existing, err := tx.FileByName(b.ID, *in.Name)
				if err == nil && existing.ID != out.ID {
					return fileError(409, "file_name_taken", "Another file uses that path.")
				}
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				if _, err := s.append(tx, &b, "file.renamed", actorOf(me), now, map[string]any{"file_id": out.ID, "before": out.Name, "after": *in.Name}); err != nil {
					return err
				}
				out.Name = *in.Name
			}
			data := map[string]any{"file_id": out.ID}
			if in.Maintained != nil {
				out.Maintained = *in.Maintained || isBriefName(out.Name)
				data["maintained"] = out.Maintained
			}
			if in.About != nil {
				out.About = []TaskRef{}
				ids := []string{}
				for _, selector := range *in.About {
					task, err := findTask(tx, b, selector)
					if err != nil {
						return err
					}
					out.About = append(out.About, taskRef(task))
					ids = append(ids, task.ID)
				}
				data["about"] = ids
			}
			if len(data) > 1 {
				if _, err := s.append(tx, &b, "file.updated", actorOf(me), now, data); err != nil {
					return err
				}
			}
		}
		if err := tx.SaveFile(out); err != nil {
			return err
		}
		return projectFile(tx, b, me, &out)
	})
	if err == nil {
		s.notify.Changed(out.BoardID)
	}
	return out, err
}

// ListFiles projects the latest version and reader-visible freshness.
func (s *Service) ListFiles(ctx context.Context, p Principal, name string, filter FileFilter) ([]File, bool, error) {
	out := []File{}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		b, me, err := s.access(tx, p, name)
		if err != nil {
			return err
		}
		out, err = tx.Files(b.ID)
		if err != nil {
			return err
		}
		var taskID string
		if filter.Task != "" {
			t, err := findTask(tx, b, filter.Task)
			if err != nil {
				return err
			}
			taskID = t.ID
		}
		selected := []File{}
		for _, f := range out {
			mine := !filter.Mine
			tagged := taskID == ""
			for _, v := range f.Versions {
				mine = mine || v.ByID == me.ID
			}
			for _, t := range f.About {
				tagged = tagged || t.ID == taskID
			}
			if mine && tagged {
				selected = append(selected, f)
			}
		}
		out = selected
		for i := range out {
			if err := projectFile(tx, b, me, &out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Maintained != out[j].Maintained {
			return out[i].Maintained
		}
		return out[i].Versions[len(out[i].Versions)-1].At > out[j].Versions[len(out[j].Versions)-1].At
	})
	more := false
	if filter.Limit > 0 && len(out) > filter.Limit {
		more = true
		out = out[:filter.Limit]
	}
	return out, more, nil
}

// FileBytes resolves a version through its board record before opening the digest.
func (s *Service) FileBytes(ctx context.Context, p Principal, name, selector, version string) (FileVersion, string, io.ReadCloser, error) {
	f, err := s.GetFile(ctx, p, name, selector)
	if err != nil {
		return FileVersion{}, "", nil, err
	}
	n := len(f.Versions)
	if version != "latest" {
		n, err = strconv.Atoi(version)
		if err != nil || n < 1 || n > len(f.Versions) {
			return FileVersion{}, "", nil, fileError(404, "version_not_found", "The file has no such version.")
		}
	}
	v := f.Versions[n-1]
	r, err := s.cfg.Blobs.Open(ctx, v.Digest)
	return v, f.Name, r, err
}

func isBriefName(name string) bool { return name == "brief.md" || name == "brief.html" }

func activeBrief(tx ReadTx, b Board) (*File, error) {
	for _, name := range []string{"brief.md", "brief.html"} {
		f, err := tx.FileByName(b.ID, name)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &f, nil
	}
	return nil, nil
}

func projectBrief(tx ReadTx, b Board, me Member) (*File, error) {
	f, err := activeBrief(tx, b)
	if err != nil || f == nil {
		return f, err
	}
	if err := projectFile(tx, b, me, f); err != nil {
		return nil, err
	}
	return f, nil
}
