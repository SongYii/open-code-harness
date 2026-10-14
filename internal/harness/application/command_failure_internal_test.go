package application

import (
	"context"
	"errors"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestUnprovenCommandTracePreservesFailureOverJoinedCancellation(t *testing.T) {
	for _, code := range []string{CodeExecOutputIncomplete, CodeExecWaitUnproven, CodeExecTeardownUnproven} {
		err := applicationError(CategoryInternal, code, true, errors.Join(context.Canceled, tools.ErrCommandTeardownUnproven))
		end := traceEnd(err)
		if end.Code != code || end.Outcome != telemetry.OutcomeFailed {
			t.Fatalf("code=%s trace=%+v", code, end)
		}
	}
}
