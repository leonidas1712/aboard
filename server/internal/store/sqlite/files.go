package sqlite

import (
	"encoding/json"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (t *tx) FileBySelector(boardID, selector string) (board.File, error) {
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT record_json FROM board_files WHERE board_id = ? AND (id = ? OR (name = ? AND removed = 0)) ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END LIMIT 1", boardID, selector, selector, selector).Scan(&raw)
	if err != nil {
		return board.File{}, notFound(err)
	}
	var out board.File
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (t *tx) Files(boardID string) ([]board.File, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT record_json FROM board_files WHERE board_id = ? AND removed = 0 ORDER BY name", boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []board.File{}
	for rows.Next() {
		var raw string
		var f board.File
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (t *tx) FileByName(boardID, name string) (board.File, error) {
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT record_json FROM board_files WHERE board_id = ? AND name = ? AND removed = 0", boardID, name).Scan(&raw)
	if err != nil {
		return board.File{}, notFound(err)
	}
	var out board.File
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (t *tx) BlobVersions() ([]board.Blob, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT record_json FROM board_files")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []board.Blob{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var f board.File
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, err
		}
		for _, v := range f.Versions {
			out = append(out, board.Blob{Digest: v.Digest, Size: v.Size})
		}
	}
	return out, rows.Err()
}

func (t *tx) SaveFile(f board.File) error {
	f.Versions = append([]board.FileVersion(nil), f.Versions...)
	for i := range f.Versions {
		f.Versions[i].By = board.Member{}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO board_files(id,board_id,name,record_json,removed) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, record_json=excluded.record_json, removed=excluded.removed", f.ID, f.BoardID, f.Name, string(raw), f.Removed)
}
