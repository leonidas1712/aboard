package delivery

import "context"

// DurableNotice retains only the nonsecret origin of an outcome or arrival.
// Content is read again with the seat's current credential before each handoff.
type DurableNotice struct {
	ID, Kind, SourceID string
	BoardID            string
	Agent              AgentRef
	Session            SessionKey
	Boot               string
	Generation         uint64
	Handed, Cancelled  bool
}

// NoticeJournal keeps permanent-seat notices separate from message sequences.
type NoticeJournal interface {
	SaveNotice(context.Context, DurableNotice) error
	Notices(context.Context, AgentRef) ([]DurableNotice, error)
	CancelNotice(context.Context, string) error
	MarkNoticeHanded(context.Context, string) error
}
