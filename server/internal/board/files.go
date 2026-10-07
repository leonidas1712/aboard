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

// File retains one board path and every version written to it.
type File struct {
	MessagesSince, TasksDoneSince, AnswersSince int

	ID, BoardID, Board, Name string
	Maintained               bool
	About                    []TaskRef
	Versions                 []FileVersion
}

// NewFile carries upload bytes and the version the caller intends to replace.
type NewFile struct {
	Name       string
	Base       int
	Maintained *bool
	About      *[]string
	MediaType  string
	Body       io.Reader
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
	if in.Name == "brief.md" || in.Name == "brief.html" {
		return out, fileError(409, "brief_path_reserved", "The brief path is reserved.")
	}
	if s.cfg.Blobs == nil {
		return out, fileError(501, "not_implemented", "This server has no blob store.")
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
		out, err = tx.FileBySelector(b.ID, in.Name)
		fresh := errors.Is(err, ErrNotFound)
		if err != nil && !fresh {
			return err
		}
		latest := 0
		if !fresh {
			latest = out.Versions[len(out.Versions)-1].Version
		}
		if latest != in.Base {
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
		if utf8.Valid(body) && fileCredential.Match(body) {
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
		e, err := s.append(tx, &b, "file.version_added", actorOf(me), now, map[string]any{"file_id": out.ID, "name": out.Name, "version": latest + 1, "digest": blob.Digest, "size": blob.Size, "media_type": media, "base_version": in.Base, "maintained": out.Maintained, "about": ids})
		if err != nil {
			return err
		}
		out.Versions = append(out.Versions, FileVersion{Version: latest + 1, Base: in.Base, Digest: blob.Digest, Size: blob.Size, MediaType: media, ByID: me.ID, By: me, At: e.At, Seq: e.Seq})
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
			if e.Type == "task.done" || e.Type == "task.cancelled" {
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
func (s *Service) FileBytes(ctx context.Context, p Principal, name, selector, version string) (FileVersion, io.ReadCloser, error) {
	f, err := s.GetFile(ctx, p, name, selector)
	if err != nil {
		return FileVersion{}, nil, err
	}
	n := len(f.Versions)
	if version != "latest" {
		n, err = strconv.Atoi(version)
		if err != nil || n < 1 || n > len(f.Versions) {
			return FileVersion{}, nil, fileError(404, "version_not_found", "The file has no such version.")
		}
	}
	v := f.Versions[n-1]
	r, err := s.cfg.Blobs.Open(ctx, v.Digest)
	return v, r, err
}
