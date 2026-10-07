package api

import "encoding/json"

// MarshalJSON preserves the difference between no brief for a member and no brief
// information for someone outside the board.
func (b Board) MarshalJSON() ([]byte, error) {
	type boardJSON Board
	if !b.OnBoard {
		return json.Marshal(boardJSON(b))
	}
	return json.Marshal(struct {
		boardJSON
		Brief *BriefSummary `json:"brief"`
	}{boardJSON: boardJSON(b), Brief: b.Brief})
}

// MarshalJSON applies Board's member-null serialization to the get response.
func (b GetBoard200JSONResponse) MarshalJSON() ([]byte, error) {
	return Board(b).MarshalJSON()
}

// MarshalJSON applies Board's member-null serialization to the creation response.
func (b CreateBoard201JSONResponse) MarshalJSON() ([]byte, error) {
	return Board(b).MarshalJSON()
}

// MarshalJSON applies Board's member-null serialization to the update response.
func (b UpdateBoard200JSONResponse) MarshalJSON() ([]byte, error) {
	return Board(b).MarshalJSON()
}
