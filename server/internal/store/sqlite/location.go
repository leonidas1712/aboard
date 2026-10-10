package sqlite

import "github.com/leonidas1712/aboard/server/internal/board"

func (t *tx) AgentLocation(memberID string) (board.AgentLocation, error) {
	var out board.AgentLocation
	err := t.queryRow("SELECT machine, harness, session_id, folder, last_active FROM agent_locations WHERE member_id = ?", memberID).Scan(&out.Machine, &out.Harness, &out.SessionID, &out.Folder, &out.LastActive)
	return out, notFound(err)
}

func (t *tx) SetAgentLocation(memberID string, loc board.AgentLocation) error {
	return t.exec(`INSERT INTO agent_locations(member_id,machine,harness,session_id,folder,last_active) VALUES (?,?,?,?,?,?)
 ON CONFLICT(member_id) DO UPDATE SET machine=excluded.machine,harness=excluded.harness,session_id=excluded.session_id,folder=excluded.folder,last_active=excluded.last_active`, memberID, loc.Machine, loc.Harness, loc.SessionID, loc.Folder, loc.LastActive)
}
