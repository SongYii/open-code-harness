package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func (service *Service) executeOneTool(ctx context.Context, owned *ownedTurn, call engine.ToolCall) (bool, RunTurnResult, error) {
	itemID, err := service.newItemID(ctx)
	if err != nil {
		if contextError(ctx) != nil {
			result, cancelErr := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			return true, result, cancelErr
		}
		result, failErr := service.failOwnedTurn(ctx, owned, "model_failure", displayFailureSentence("model_failure"))
		return true, result, failErr
	}
	owned.toolItemID = itemID
	owned.toolCallID = call.ID
	owned.toolName = call.Name
	owned.approvalID = ""

	started, err := domain.Decide(owned.state, domain.StartToolCall{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: itemID,
		CallID: call.ID, Name: call.Name, Arguments: call.Arguments, StepIndex: owned.stepIndex,
	})
	if err != nil {
		return true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := service.commitStepAppend(ctx, owned, started); err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	_ = owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeToolExecutionStarted, Text: runtimeToolText(call)})
	if err := contextError(ctx); err != nil {
		result, cancelErr := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		return true, result, cancelErr
	}

	spec, ok := service.catalog.Spec(call.Name)
	if !ok {
		return service.failToolAndContinue(ctx, owned, call, CodeUnknownTool, ToolTextUnknownTool)
	}
	if err := tools.ValidateArgs(spec, call.Arguments); err != nil {
		return service.failToolAndContinue(ctx, owned, call, CodeInvalidArgs, ToolTextInvalidArgs)
	}
	args, err := parseToolArgs(spec.Name, call.Arguments)
	if err != nil {
		return service.failToolAndContinue(ctx, owned, call, CodeInvalidArgs, ToolTextInvalidArgs)
	}

	requested := args.scopePath(owned.state.WorkspaceRoot)
	if requested != "" {
		scope, scopeErr := tools.CheckScopeLexical(tools.ScopeRequest{WorkspaceRoot: owned.state.WorkspaceRoot, Requested: requested})
		if scopeErr != nil || !scope.InWorkspace {
			return service.failToolAndContinue(ctx, owned, call, CodeScopeDenied, ToolTextScopeDenied)
		}
	}
	if extra := args.execBinaryPath(); extra != "" {
		scope, scopeErr := tools.CheckScopeLexical(tools.ScopeRequest{WorkspaceRoot: owned.state.WorkspaceRoot, Requested: extra})
		if scopeErr != nil || !scope.InWorkspace {
			return service.failToolAndContinue(ctx, owned, call, CodeScopeDenied, ToolTextScopeDenied)
		}
	}

	workspaceIn := true
	resolved := ""
	if requested != "" && !isNilValue(service.files) {
		abs, resolveErr := service.files.Resolve(ctx, owned.state.WorkspaceRoot, requested)
		if contextError(ctx) != nil || isCancelCause(resolveErr) {
			result, cancelErr := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			return true, result, cancelErr
		}
		if resolveErr != nil || abs == "" {
			workspaceIn = false
		} else {
			resolved = abs
		}
	}
	if extra := args.execBinaryPath(); extra != "" && !isNilValue(service.files) {
		abs, resolveErr := service.files.Resolve(ctx, owned.state.WorkspaceRoot, extra)
		if contextError(ctx) != nil || isCancelCause(resolveErr) {
			result, cancelErr := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			return true, result, cancelErr
		}
		if resolveErr != nil || abs == "" {
			workspaceIn = false
		}
	}

	decision, decideErr := service.policy.Decide(policy.Input{
		Name: spec.Name, Risk: spec.Risk, Mutates: spec.Mutates,
		WorkspaceIn: workspaceIn, PathLiteral: args.pathLiteral(),
	})
	if decideErr != nil {
		decision = policy.Decision{Effect: policy.EffectDeny, RuleID: policy.RuleUnknownRisk, Reason: policy.ReasonUnknownRisk}
	}
	recorded, err := domain.Decide(owned.state, domain.RecordPolicyDecision{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: itemID,
		CallID: call.ID, Name: spec.Name, Effect: string(decision.Effect), RuleID: decision.RuleID, Reason: decision.Reason,
	})
	if err != nil {
		return true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := service.commitStepAppend(ctx, owned, recorded); err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	if contextError(ctx) != nil {
		result, cancelErr := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		return true, result, cancelErr
	}
	if !workspaceIn {
		return service.failToolAndContinue(ctx, owned, call, CodeScopeDenied, ToolTextScopeDenied)
	}

	switch decision.Effect {
	case policy.EffectDeny:
		return service.failToolAndContinue(ctx, owned, call, CodePolicyDenied, ToolTextPolicyDenied)
	case policy.EffectRequireApproval:
		granted, done, result, waitErr := service.waitApproval(ctx, owned, call, decision)
		if done {
			return true, result, waitErr
		}
		if !granted {
			return false, owned.result, nil
		}
	case policy.EffectAllow:
	default:
		return service.failToolAndContinue(ctx, owned, call, CodePolicyDenied, ToolTextPolicyDenied)
	}
	return service.runToolBody(ctx, owned, spec, call, args, resolved)
}

func (service *Service) waitApproval(ctx context.Context, owned *ownedTurn, call engine.ToolCall, decision policy.Decision) (bool, bool, RunTurnResult, error) {
	approvalID, sourceErr := service.ids.NewApprovalID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		if contextError(ctx) != nil {
			result, err := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			return false, true, result, err
		}
		return false, true, cloneRunTurnResult(owned.result), mapped
	}
	if _, err := domain.ParseApprovalID(string(approvalID)); err != nil {
		return false, true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	owned.approvalID = approvalID
	requested, err := domain.Decide(owned.state, domain.RequestApproval{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: owned.toolItemID,
		ApprovalID: approvalID, CallID: call.ID, Name: call.Name, Reason: decision.Reason,
	})
	if err != nil {
		return false, true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := service.commitStepAppend(ctx, owned, requested); err != nil {
		return false, true, cloneRunTurnResult(owned.result), err
	}
	_ = owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeApprovalRequested, Text: runtimeToolText(call)})

	waitCtx, cancel := context.WithTimeout(ctx, service.config.ApprovalTimeout)
	defer cancel()
	answer, approveErr := service.approver.Decide(waitCtx, tools.ApprovalRequest{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ApprovalID: approvalID,
		Name: call.Name, CallID: call.ID, Arguments: call.Arguments, Reason: decision.Reason,
	})
	if contextError(ctx) != nil {
		result, err := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		return false, true, result, err
	}

	resolvedDecision := domain.ApprovalDecisionDenied
	failCode, failText := CodeApprovalDenied, ToolTextApprovalDenied
	if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
		resolvedDecision = domain.ApprovalDecisionTimeout
		failCode, failText = CodeApprovalTimeout, ToolTextApprovalTimeout
	} else if approveErr == nil && answer.Granted {
		resolvedDecision = domain.ApprovalDecisionGranted
	}
	resolved, err := domain.Decide(owned.state, domain.ResolveApproval{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: owned.toolItemID,
		ApprovalID: approvalID, Decision: resolvedDecision,
	})
	if err != nil {
		return false, true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := service.commitStepAppend(ctx, owned, resolved); err != nil {
		return false, true, cloneRunTurnResult(owned.result), err
	}
	owned.approvalID = ""
	_ = owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeApprovalResolved, Text: runtimeToolText(call)})
	if resolvedDecision != domain.ApprovalDecisionGranted {
		done, result, failErr := service.failToolAndContinue(ctx, owned, call, failCode, failText)
		return false, done, result, failErr
	}
	return true, false, owned.result, nil
}

func (service *Service) runToolBody(ctx context.Context, owned *ownedTurn, spec domain.ToolSpec, call engine.ToolCall, args toolArgs, resolved string) (bool, RunTurnResult, error) {
	if _, ok := owned.started[owned.toolItemID]; !ok {
		return true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "tool_started_uncommitted", false, nil)
	}
	if _, ok := owned.executed[owned.toolItemID]; ok {
		return false, owned.result, nil
	}
	if contextError(ctx) != nil {
		result, err := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		return true, result, err
	}
	owned.executed[owned.toolItemID] = struct{}{}

	content, truncated, failCode, failText, execErr := service.invokeTool(ctx, spec, args, resolved)
	if isCancelCause(execErr) || contextError(ctx) != nil {
		result, err := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		return true, result, err
	}
	if failCode != "" {
		return service.failToolAndContinue(ctx, owned, call, failCode, failText)
	}
	if execErr != nil {
		return service.failToolAndContinue(ctx, owned, call, CodeInvalidArgs, ToolTextInvalidArgs)
	}
	return service.completeToolAndContinue(ctx, owned, call, content, truncated)
}

func (service *Service) invokeTool(ctx context.Context, spec domain.ToolSpec, args toolArgs, resolved string) (string, bool, string, string, error) {
	switch spec.Name {
	case tools.NameReadFile:
		data, truncated, err := service.files.Read(ctx, resolved, MaxToolResultBytes)
		if err != nil {
			return "", false, "", "", err
		}
		if !utf8.Valid(data) {
			return "", false, CodeInvalidArgs, ToolTextInvalidArgs, nil
		}
		text := string(data)
		if truncated {
			return appendTruncation(text), true, "", "", nil
		}
		return text, false, "", "", nil
	case tools.NameWriteFile:
		if err := service.files.Write(ctx, resolved, []byte(args.Content)); err != nil {
			return "", false, "", "", err
		}
		return fmt.Sprintf("wrote %d bytes", len(args.Content)), false, "", "", nil
	case tools.NameListDir:
		names, truncated, err := service.files.List(ctx, resolved, args.depthOrDefault(), tools.MaxListDirEntries)
		if err != nil {
			return "", false, "", "", err
		}
		text := strings.Join(names, "\n")
		if truncated {
			return appendTruncation(text), true, "", "", nil
		}
		return text, false, "", "", nil
	case tools.NameExec:
		if isNilValue(service.commands) {
			return "", false, CodeInvalidArgs, ToolTextInvalidArgs, nil
		}
		result, err := service.commands.Run(ctx, tools.CommandSpec{
			Argv: append([]string(nil), args.Argv...), Cwd: resolved, Timeout: DefaultExecTimeout, MaxBytes: MaxToolResultBytes,
		})
		if err != nil {
			return "", false, "", "", err
		}
		if result.TimedOut {
			return "", false, CodeExecTimeout, ToolTextExecTimeout, nil
		}
		text := fmt.Sprintf("exit %d\n%s", result.ExitCode, result.Output)
		if result.Truncated {
			return appendTruncation(text), true, "", "", nil
		}
		return text, false, "", "", nil
	default:
		return "", false, CodeUnknownTool, ToolTextUnknownTool, nil
	}
}

func (service *Service) failToolAndContinue(ctx context.Context, owned *ownedTurn, call engine.ToolCall, code, message string) (bool, RunTurnResult, error) {
	decided, err := domain.Decide(owned.state, domain.FailToolCall{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: owned.toolItemID,
		CallID: call.ID, Code: code, Message: message,
	})
	if err != nil {
		return true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := service.commitStepAppend(ctx, owned, decided); err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	_ = owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeToolExecutionFailed, Code: code})
	owned.approvalID = ""
	return false, owned.result, nil
}

func (service *Service) completeToolAndContinue(ctx context.Context, owned *ownedTurn, call engine.ToolCall, content string, truncated bool) (bool, RunTurnResult, error) {
	decided, err := domain.Decide(owned.state, domain.CompleteToolCall{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: owned.toolItemID,
		CallID: call.ID, Content: content, Truncated: truncated,
	})
	if err != nil {
		return true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := service.commitStepAppend(ctx, owned, decided); err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	_ = owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeToolExecutionCompleted, Text: runtimeToolText(call)})
	owned.approvalID = ""
	return false, owned.result, nil
}

func appendTruncation(text string) string {
	return text + TruncationMarker
}

func runtimeToolText(call engine.ToolCall) string {
	if call.Name == "" {
		return call.ID
	}
	if call.ID == "" {
		return call.Name
	}
	return call.Name + ":" + call.ID
}

func isCancelCause(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || IsCategory(err, CategoryCanceled))
}

type toolArgs struct {
	Path    string   `json:"path"`
	Content string   `json:"content"`
	Depth   *int     `json:"depth"`
	Argv    []string `json:"argv"`
	Cwd     string   `json:"cwd"`
}

func parseToolArgs(name, raw string) (toolArgs, error) {
	var args toolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return toolArgs{}, err
	}
	switch name {
	case tools.NameReadFile, tools.NameWriteFile, tools.NameListDir:
		if args.Path == "" {
			return toolArgs{}, argsError()
		}
	case tools.NameExec:
		if len(args.Argv) == 0 {
			return toolArgs{}, argsError()
		}
	}
	return args, nil
}

func argsError() error { return errors.New("invalid tool arguments") }

func (args toolArgs) scopePath(workspace string) string {
	if args.Path != "" {
		return args.Path
	}
	if args.Cwd != "" {
		return args.Cwd
	}
	if len(args.Argv) > 0 {
		return "."
	}
	return workspace
}

func (args toolArgs) pathLiteral() string {
	if args.Path != "" {
		return args.Path
	}
	return args.Cwd
}

func (args toolArgs) execBinaryPath() string {
	if len(args.Argv) == 0 {
		return ""
	}
	binary := args.Argv[0]
	if strings.Contains(binary, "/") || strings.Contains(binary, "\\") {
		return binary
	}
	return ""
}

func (args toolArgs) depthOrDefault() int {
	if args.Depth == nil {
		return tools.DefaultListDirDepth
	}
	return *args.Depth
}
