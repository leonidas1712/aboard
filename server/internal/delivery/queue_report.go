package delivery

import (
	"context"
	"errors"
)

// ErrQueueReportConflict ends a reporter whose server fence was replaced.
var ErrQueueReportConflict = errors.New("queue reporter fence changed")

// QueueFence is the own-seat server fence, without message bodies or credentials.
type QueueFence struct {
	BoardID, MemberID, CredentialGeneration string
	Epoch, Revision                         int64
}

// QueueReportIntent is an immutable claim or update retained before its request.
type QueueReportIntent struct {
	Session, Boot, IdempotencyKey, CredentialGeneration string
	ExpectedEpoch                                       *int64
	Epoch, Revision                                     int64
	Messages                                            []QueuedMessage
}

// QueueReportServer publishes observational receipts using only the seat's credential.
type QueueReportServer interface {
	QueueFence(context.Context, AgentRef) (QueueFence, error)
	ReportQueue(context.Context, AgentRef, QueueReportIntent) (QueueFence, error)
}

// QueueReporter retains a lease and an in-flight immutable request across restart.
type QueueReporter struct {
	CredentialGeneration string
	Agent                AgentRef
	Session              SessionKey
	Boot                 string
	Generation           uint64
	Epoch, Revision      int64
	Pending              *QueueReportIntent
	Stopped              bool
}

// QueueReportJournal fences report intents against the current local seat binding.
type QueueReportJournal interface {
	QueueReporter(context.Context, AgentRef, SessionKey, string, uint64) (QueueReporter, error)
	SaveQueueReporter(context.Context, QueueReporter) error
}
