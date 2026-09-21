package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func (service *Service) toolSchemas(state domain.Session) []domain.ToolSchema {
	if !service.catalogEnabled() {
		return nil
	}
	if state.Parent == nil {
		return service.catalog.Schemas()
	}
	specs := service.catalog.Specs()
	schemas := make([]domain.ToolSchema, 0, 2)
	for _, spec := range specs {
		if childSchemaAllowed(spec.Name) {
			schemas = append(schemas, domain.ToolSchema{Name: spec.Name, Description: spec.Description, InputSchema: append([]byte(nil), spec.InputSchema...)})
		}
	}
	return schemas
}

func childSchemaAllowed(name string) bool {
	return name == tools.NameReadFile || name == tools.NameListDir
}

func childDispatchAllowed(name string) bool {
	return name == tools.NameReadFile || name == tools.NameListDir
}

type discardRuntimeSink struct{}

func (discardRuntimeSink) Emit(context.Context, engine.RuntimeEvent) error { return nil }

func (service *Service) invokeDelegateTask(ctx context.Context, owned *ownedTurn, task string) (string, bool, string, string, error) {
	if owned.state.Parent != nil {
		return "", false, CodeSubagentCapabilityDenied, ToolTextSubagentCapabilityDenied, nil
	}
	if !utf8.ValidString(task) || strings.TrimSpace(task) == "" {
		return "", false, CodeInvalidArgs, ToolTextInvalidArgs, nil
	}
	childCtx, cancel := context.WithTimeout(ctx, service.config.Subagents.Timeout)
	defer cancel()
	created, err := service.CreateSession(childCtx, CreateSessionRequest{
		WorkspaceRoot: owned.state.WorkspaceRoot,
		Parent: &domain.SessionParent{
			SessionID: owned.result.SessionID,
			TurnID:    owned.result.TurnID,
			ItemID:    owned.toolItemID,
			CallID:    owned.toolCallID,
		},
	})
	if err != nil {
		if contextError(ctx) != nil {
			return "", false, "", "", contextError(ctx)
		}
		if errors.Is(childCtx.Err(), context.DeadlineExceeded) {
			return "", false, CodeSubagentTimeout, ToolTextSubagentTimeout, nil
		}
		return "", false, CodeSubagentFailed, ToolTextSubagentFailed, nil
	}
	requestID := domain.RunTurnRequestID("subagent-request-" + string(created.SessionID))
	result, err := service.RunTurn(childCtx, RunTurnRequest{SessionID: created.SessionID, RequestID: requestID, Input: task, Sink: discardRuntimeSink{}})
	if err != nil {
		if contextError(ctx) != nil {
			return "", false, "", "", contextError(ctx)
		}
		if errors.Is(childCtx.Err(), context.DeadlineExceeded) {
			return "", false, CodeSubagentTimeout, ToolTextSubagentTimeout, nil
		}
		return "", false, CodeSubagentFailed, ToolTextSubagentFailed, nil
	}
	text := fmt.Sprintf("child session: %s\n%s", created.SessionID, result.Text)
	if len(text) > MaxToolResultBytes {
		text = truncateValidUTF8(text, MaxToolResultBytes)
		return appendTruncation(text), true, "", "", nil
	}
	return text, false, "", "", nil
}
