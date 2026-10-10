package board

import (
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Allowance records the standing admission categories and revision for one person.
type Allowance struct {
	ID, PersonID string
	Revision     int
	Categories   []string
}

// InvitePairingInput names an initiating seat and proposed work without carrying secrets.
type InvitePairingInput struct{ InitiatingAgentID, Work string }

// InvitePeopleInput freezes the requested lifetime and bundled admissions.
type InvitePeopleInput struct {
	SuggestedHandle string
	TTLSeconds      *int
	Boards          []string
	Pairing         *InvitePairingInput
}

// AdminAction contains one exact administrative operation, addressed by immutable ids.
type AdminAction struct {
	Kind, BoardID, PersonID, Role, KeyID string
	Invite                               *InvitePeopleInput
	Policy                               *rules.PolicyChange
}

// AdminAuthorization records the person and scoped authority used by the acting agent.
type AdminAuthorization struct {
	Kind, PersonID, AgentID, ParentKeyID, Via, AllowanceID, ApprovalID, PayloadHash string
	AllowanceRevision                                                               int
}

// AdminExecution is the immutable nonsecret receipt saved with an executed action.
type AdminExecution struct {
	At            string
	Decision      string
	Authorization AdminAuthorization
	InviteID      string
}

// Approval freezes an agent request and retains its decision and execution history.
type Approval struct {
	ID, PersonID, AgentID, ParentKeyID, PayloadHash, State, CreatedAt, RequestKey string
	ExpiresAt, DecidedAt                                                          *string
	Action                                                                        AdminAction
	Execution                                                                     *AdminExecution
}

// AdminActionResult separates a held request from an executed action and one-time invite secret.
type AdminActionResult struct {
	State       string
	Approval    Approval
	Invite      *NewServerInvite
	TouchedKeys []string
}

// AdminActionJSON is the frozen wire payload used for approval hashing and storage.
func AdminActionJSON(a AdminAction) map[string]any {
	out := map[string]any{"kind": a.Kind}
	switch a.Kind {
	case "invite_people":
		in := map[string]any{}
		if a.Invite != nil {
			if a.Invite.SuggestedHandle != "" {
				in["suggested_handle"] = a.Invite.SuggestedHandle
			}
			if a.Invite.TTLSeconds != nil {
				in["ttl_seconds"] = *a.Invite.TTLSeconds
			}
			if a.Invite.Boards != nil {
				in["boards"] = a.Invite.Boards
			}
			if a.Invite.Pairing != nil {
				in["pairing"] = map[string]any{"initiating_agent_id": a.Invite.Pairing.InitiatingAgentID, "work": a.Invite.Pairing.Work}
			}
		}
		out["invite"] = in
	case "add_people":
		out["board_id"], out["person_id"] = a.BoardID, a.PersonID
	case "set_server_role":
		out["person_id"], out["role"] = a.PersonID, a.Role
	case "set_board_role":
		out["board_id"], out["person_id"], out["role"] = a.BoardID, a.PersonID, a.Role
	case "remove_person":
		out["person_id"] = a.PersonID
		if a.BoardID != "" {
			out["board_id"] = a.BoardID
		}
	case "revoke_key":
		out["key_id"] = a.KeyID
	case "set_board_policy":
		out["board_id"], out["policy"] = a.BoardID, a.Policy
	}
	return out
}

// AdminActionFromJSON decodes the canonical persisted payload, never credentials.
func AdminActionFromJSON(raw []byte) (AdminAction, error) {
	var in struct {
		Kind     string              `json:"kind"`
		BoardID  string              `json:"board_id"`
		PersonID string              `json:"person_id"`
		Role     string              `json:"role"`
		KeyID    string              `json:"key_id"`
		Policy   *rules.PolicyChange `json:"policy"`
		Invite   *struct {
			SuggestedHandle string   `json:"suggested_handle"`
			TTLSeconds      *int     `json:"ttl_seconds"`
			Boards          []string `json:"boards"`
			Pairing         *struct {
				InitiatingAgentID string `json:"initiating_agent_id"`
				Work              string `json:"work"`
			} `json:"pairing"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return AdminAction{}, fmt.Errorf("decode admin action: %w", err)
	}
	a := AdminAction{Kind: in.Kind, BoardID: in.BoardID, PersonID: in.PersonID, Role: in.Role, KeyID: in.KeyID, Policy: in.Policy}
	if in.Invite != nil {
		a.Invite = &InvitePeopleInput{SuggestedHandle: in.Invite.SuggestedHandle, TTLSeconds: in.Invite.TTLSeconds, Boards: in.Invite.Boards}
		if in.Invite.Pairing != nil {
			a.Invite.Pairing = &InvitePairingInput{InitiatingAgentID: in.Invite.Pairing.InitiatingAgentID, Work: in.Invite.Pairing.Work}
		}
	}
	return a, nil
}

func (a AdminAuthorization) data() map[string]any {
	out := map[string]any{"person_id": a.PersonID, "agent_id": a.AgentID, "parent_key_id": a.ParentKeyID, "via": a.Via, "payload_hash": a.PayloadHash}
	if a.Kind != "" {
		out["kind"] = a.Kind
	}
	if a.Via == "allowance" {
		out["allowance_id"], out["allowance_revision"] = a.AllowanceID, a.AllowanceRevision
	} else {
		out["approval_id"] = a.ApprovalID
	}
	return out
}

func actionHash(a AdminAction) (string, error) {
	raw, err := events.Canonical(AdminActionJSON(a))
	return events.HashBytes(raw), err
}

func authorizationKind(a AdminAction) string {
	switch a.Kind {
	case "invite_people":
		return "invited"
	case "add_people":
		return "added"
	case "set_server_role", "set_board_role":
		return "role_changed"
	}
	return ""
}
