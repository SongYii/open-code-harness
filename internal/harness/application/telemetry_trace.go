package application

import (
	"context"
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
)

func traceString(key telemetry.Key, value string) []telemetry.Attribute {
	if !telemetry.IsSafeString(value) {
		return nil
	}
	return []telemetry.Attribute{telemetry.String(key, value)}
}

func traceIDs(session domain.SessionID, turn domain.TurnID, item domain.ItemID) []telemetry.Attribute {
	attributes := traceString(telemetry.KeySessionID, string(session))
	attributes = append(attributes, traceString(telemetry.KeyTurnID, string(turn))...)
	attributes = append(attributes, traceString(telemetry.KeyItemID, string(item))...)
	return attributes
}

func traceEnd(err error, attributes ...telemetry.Attribute) telemetry.End {
	end := telemetry.End{Outcome: telemetry.OutcomeOK, Attributes: attributes}
	if err == nil {
		return end
	}
	end.Outcome = telemetry.OutcomeFailed
	var appErr *Error
	if errors.As(err, &appErr) && appErr != nil {
		end.Code = appErr.Code
		if appErr.Category == CategoryCanceled {
			end.Outcome = telemetry.OutcomeCanceled
		}
	}
	if errors.Is(err, context.Canceled) {
		end.Outcome = telemetry.OutcomeCanceled
		if end.Code == "" {
			end.Code = "canceled"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		end.Outcome = telemetry.OutcomeTimeout
		if end.Code == "" {
			end.Code = "timeout"
		}
	}
	if end.Code == "" {
		end.Code = "operation_failed"
	}
	return end
}

func modelTraceStart(request engine.ModelRequest) telemetry.Start {
	attributes := traceIDs(request.SessionID, request.TurnID, request.ItemID)
	purpose := request.Purpose
	if purpose == "" {
		purpose = engine.ModelRequestPurposeConversation
	}
	attributes = append(attributes, traceString(telemetry.KeyModelPurpose, string(purpose))...)
	return telemetry.Start{Kind: telemetry.KindModelRequest, Attributes: attributes}
}

func modelStatsAttributes(stats engine.AttemptStats) []telemetry.Attribute {
	var attributes []telemetry.Attribute
	if telemetry.IsSafeString(stats.FinishReason) {
		attributes = append(attributes, telemetry.String(telemetry.KeyModelFinish, stats.FinishReason))
	}
	if stats.Usage != nil {
		attributes = append(attributes,
			telemetry.Uint64(telemetry.KeyUsageInputTokens, stats.Usage.InputTokens),
			telemetry.Uint64(telemetry.KeyUsageOutputTokens, stats.Usage.OutputTokens),
			telemetry.Uint64(telemetry.KeyUsageCachedTokens, stats.Usage.CachedInputTokens),
		)
	}
	return attributes
}

func (service *Service) runModel(ctx context.Context, request engine.RunRequest, emitter *engine.Emitter) (result engine.RunResult, returnErr error) {
	traceCtx, span := telemetry.SafeStart(service.telemetry, ctx, modelTraceStart(request.ModelRequest))
	defer func() { span.End(traceEnd(returnErr, modelStatsAttributes(result.Stats)...)) }()
	return service.runner.Run(traceCtx, request, emitter)
}

type logicalAppendTrace struct{ span telemetry.Span }

func startAppendTrace(ctx context.Context, tracer telemetry.Tracer, intent AppendIntent) (context.Context, *logicalAppendTrace) {
	attributes := traceString(telemetry.KeyAppendID, string(intent.Request.AppendID))
	attributes = append(attributes, traceString(telemetry.KeySessionID, string(intent.Request.SessionID))...)
	attributes = append(attributes,
		telemetry.Uint64(telemetry.KeyAppendEventCount, uint64(len(intent.Request.Events))),
		telemetry.Uint64(telemetry.KeyAppendExpectedVersion, intent.Request.ExpectedVersion),
	)
	traceCtx, span := telemetry.SafeStart(tracer, ctx, telemetry.Start{Kind: telemetry.KindStoreAppend, Attributes: attributes})
	return traceCtx, &logicalAppendTrace{span: span}
}

func (trace *logicalAppendTrace) end(err error) {
	if trace != nil && trace.span != nil {
		trace.span.End(traceEnd(err))
	}
}
