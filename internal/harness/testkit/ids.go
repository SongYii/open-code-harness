package testkit

import (
	"fmt"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// SequenceIDs is a deterministic, concurrent-safe ID generator. Each ID type
// advances independently.
type SequenceIDs struct {
	mu sync.Mutex

	sessions  uint64
	turns     uint64
	items     uint64
	commands  uint64
	appends   uint64
	events    uint64
	approvals uint64
}

var _ application.IDGenerator = (*SequenceIDs)(nil)

func NewSequenceIDs() *SequenceIDs { return &SequenceIDs{} }

func (ids *SequenceIDs) NewSessionID() (domain.SessionID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.sessions++
	return domain.SessionID(fmt.Sprintf("session-%d", ids.sessions)), nil
}

func (ids *SequenceIDs) NewTurnID() (domain.TurnID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.turns++
	return domain.TurnID(fmt.Sprintf("turn-%d", ids.turns)), nil
}

func (ids *SequenceIDs) NewItemID() (domain.ItemID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.items++
	return domain.ItemID(fmt.Sprintf("item-%d", ids.items)), nil
}

func (ids *SequenceIDs) NewCommandID() (domain.CommandID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.commands++
	return domain.CommandID(fmt.Sprintf("command-%d", ids.commands)), nil
}

func (ids *SequenceIDs) NewAppendID() (domain.AppendID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.appends++
	return domain.AppendID(fmt.Sprintf("append-%d", ids.appends)), nil
}

func (ids *SequenceIDs) NewEventID() (domain.EventID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.events++
	return domain.EventID(fmt.Sprintf("event-%d", ids.events)), nil
}

func (ids *SequenceIDs) NewApprovalID() (domain.ApprovalID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.approvals++
	return domain.ApprovalID(fmt.Sprintf("approval-%d", ids.approvals)), nil
}
