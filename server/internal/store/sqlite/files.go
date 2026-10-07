package sqlite

import (
	"encoding/json"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (t *tx) FileBySelector(boardID, selector string) (board.File, error) {
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT record_json FROM board_files WHERE board_id = ? AND (id = ? OR name = ?)", boardID, selector, selector).Scan(&raw)
	if err != nil {
		return board.File{}, notFound(err)
	}
	var out board.File
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (t *tx) Files(boardID string) ([]board.File, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT record_json FROM board_files WHERE board_id = ? ORDER BY name", boardID)
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

func (t *tx) SaveFile(f board.File) error {
	f.Versions = append([]board.FileVersion(nil), f.Versions...)
	for i := range f.Versions {
		f.Versions[i].By = board.Member{}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO board_files(id,board_id,name,record_json) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, record_json=excluded.record_json", f.ID, f.BoardID, f.Name, string(raw))
}
