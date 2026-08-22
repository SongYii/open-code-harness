package tools

import (
	"context"
	"sync"
)

// Slot is a replaceable Approver. Composition installs one at construction
// (initially DenyApprover). An ACP server sets itself for the life of Serve
// and restores deny on teardown. Decide always calls the current inner value;
// a nil inner is treated as deny.
type Slot struct {
	mu    sync.Mutex
	inner Approver
}

func NewSlot(inner Approver) *Slot {
	if isNilApprover(inner) {
		inner = DenyApprover{}
	}
	return &Slot{inner: inner}
}

func (slot *Slot) Set(inner Approver) {
	if slot == nil {
		return
	}
	if isNilApprover(inner) {
		inner = DenyApprover{}
	}
	slot.mu.Lock()
	slot.inner = inner
	slot.mu.Unlock()
}

func (slot *Slot) Decide(ctx context.Context, req ApprovalRequest) (ApprovalAnswer, error) {
	if slot == nil {
		return ApprovalAnswer{}, nil
	}
	slot.mu.Lock()
	inner := slot.inner
	slot.mu.Unlock()
	if isNilApprover(inner) {
		return DenyApprover{}.Decide(ctx, req)
	}
	return inner.Decide(ctx, req)
}

func isNilApprover(value Approver) bool {
	return value == nil
}

var _ Approver = (*Slot)(nil)
