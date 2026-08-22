package tools

import (
	"context"
	"testing"
)

func TestSlotStartsDeniedAndCanBeReplaced(t *testing.T) {
	slot := NewSlot(nil)
	denied, err := slot.Decide(context.Background(), ApprovalRequest{Name: "write_file"})
	if err != nil || denied.Granted {
		t.Fatalf("empty slot = %#v, %v; want deny", denied, err)
	}

	slot.Set(grantApprover{})
	granted, err := slot.Decide(context.Background(), ApprovalRequest{Name: "write_file"})
	if err != nil || !granted.Granted {
		t.Fatalf("replaced slot = %#v, %v; want grant", granted, err)
	}

	slot.Set(nil)
	denied, err = slot.Decide(context.Background(), ApprovalRequest{Name: "write_file"})
	if err != nil || denied.Granted {
		t.Fatalf("restored slot = %#v, %v; want deny", denied, err)
	}
}

type grantApprover struct{}

func (grantApprover) Decide(context.Context, ApprovalRequest) (ApprovalAnswer, error) {
	return ApprovalAnswer{Granted: true}, nil
}
