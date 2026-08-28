package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func CanonicalWorkspaceRoot(root string) (string, error) {
	if !utf8.ValidString(root) || strings.TrimSpace(root) != root || !filepath.IsAbs(root) {
		return "", errors.New("workspace root must be an absolute path without surrounding whitespace")
	}
	return filepath.Clean(root), nil
}

type ListSessionsRequest struct {
	WorkspaceRoot string
	Cursor        string
}

type ListedSession struct {
	SessionID     domain.SessionID
	WorkspaceRoot string
	UpdatedAt     time.Time
}

type ListSessionsResult struct {
	Sessions   []ListedSession
	NextCursor string
}

type CreateSessionRequest struct{ WorkspaceRoot string }
type CreateSessionResult struct {
	SessionID domain.SessionID
	Records   []domain.RecordedEvent
}
type ResumeSessionRequest struct {
	SessionID     domain.SessionID
	WorkspaceRoot string
}

type DeleteSessionRequest struct {
	SessionID     domain.SessionID
	WorkspaceRoot string
}

type CloseSessionRequest struct{ SessionID domain.SessionID }
type CloseSessionResult struct {
	Session domain.Session
	Records []domain.RecordedEvent
}

type sessionHeadCatalog interface {
	ListSessionHeads(context.Context, ListSessionHeadsRequest) (SessionHeadPage, error)
}

func (service *Service) ListSessions(ctx context.Context, request ListSessionsRequest) (ListSessionsResult, error) {
	if service == nil {
		return ListSessionsResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	workspaceRoot, err := CanonicalWorkspaceRoot(request.WorkspaceRoot)
	if err != nil {
		return ListSessionsResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	if err := contextError(ctx); err != nil {
		return ListSessionsResult{}, err
	}
	catalog, ok := service.store.(sessionHeadCatalog)
	if !ok {
		return ListSessionsResult{}, storeContractViolation(errors.New("event store does not implement session head catalog"))
	}
	page, err := catalog.ListSessionHeads(ctx, ListSessionHeadsRequest{
		WorkspaceRoot: workspaceRoot,
		Cursor:        request.Cursor,
		Limit:         50,
	})
	if err != nil {
		if contextErr := contextError(ctx); contextErr != nil {
			return ListSessionsResult{}, contextErr
		}
		if IsStoreCode(err, StoreCodeInvalidRead) {
			return ListSessionsResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
		}
		return ListSessionsResult{}, applicationError(CategoryPersistence, "list_failed", false, err)
	}
	if err := contextError(ctx); err != nil {
		return ListSessionsResult{}, err
	}
	if len(page.Sessions) > 50 || (len(page.Sessions) == 0 && page.NextCursor != "") {
		return ListSessionsResult{}, storeContractViolation(errors.New("invalid session head page"))
	}
	result := ListSessionsResult{
		Sessions:   make([]ListedSession, len(page.Sessions)),
		NextCursor: page.NextCursor,
	}
	for index, head := range page.Sessions {
		if _, err := domain.ParseSessionID(string(head.SessionID)); err != nil {
			return ListSessionsResult{}, storeContractViolation(err)
		}
		root, err := CanonicalWorkspaceRoot(head.WorkspaceRoot)
		if err != nil || root != workspaceRoot || head.WorkspaceRoot != root {
			return ListSessionsResult{}, storeContractViolation(errors.New("invalid session head workspace"))
		}
		switch head.Status {
		case SessionHeadStatusIdle, SessionHeadStatusRunning, SessionHeadStatusClosed:
		default:
			return ListSessionsResult{}, storeContractViolation(errors.New("invalid session head status"))
		}
		if head.UpdatedAt.IsZero() || head.UpdatedAt.Location() != time.UTC {
			return ListSessionsResult{}, storeContractViolation(errors.New("invalid session head timestamp"))
		}
		result.Sessions[index] = ListedSession{
			SessionID:     head.SessionID,
			WorkspaceRoot: root,
			UpdatedAt:     head.UpdatedAt,
		}
	}
	return result, nil
}

func (service *Service) CreateSession(ctx context.Context, request CreateSessionRequest) (CreateSessionResult, error) {
	if service == nil {
		return CreateSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	workspaceRoot, err := CanonicalWorkspaceRoot(request.WorkspaceRoot)
	if err != nil {
		return CreateSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	if err := contextError(ctx); err != nil {
		return CreateSessionResult{}, err
	}
	sessionID, err := service.ids.NewSessionID()
	if mapped := generatedIDError(ctx, err); mapped != nil {
		return CreateSessionResult{}, mapped
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return CreateSessionResult{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	commandID, err := service.ids.NewCommandID()
	if mapped := generatedIDError(ctx, err); mapped != nil {
		return CreateSessionResult{}, mapped
	}
	if _, err := domain.ParseCommandID(string(commandID)); err != nil {
		return CreateSessionResult{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	decided, err := domain.Decide(domain.Session{}, domain.CreateSession{SessionID: sessionID, WorkspaceRoot: workspaceRoot})
	if err != nil {
		return CreateSessionResult{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	next, records, err := appendCompact(ctx, service, sessionID, domain.Session{}, decided, commandID, nil)
	if err != nil {
		return CreateSessionResult{}, err
	}
	if next.ID != sessionID {
		return CreateSessionResult{}, storeContractViolation(nil)
	}
	return CreateSessionResult{SessionID: sessionID, Records: records}, nil
}

func (service *Service) LoadSession(ctx context.Context, sessionID domain.SessionID) (domain.Session, error) {
	state, err := service.loadLifecycleSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if state.Status == domain.SessionStatusDeleted {
		return domain.Session{}, applicationError(CategoryValidation, "session_not_found", false, nil)
	}
	return state, nil
}

func (service *Service) loadLifecycleSession(ctx context.Context, sessionID domain.SessionID) (domain.Session, error) {
	if service == nil {
		return domain.Session{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	return loadCompactSessionPinned(ctx, service.store, sessionID)
}

func (service *Service) ResumeSession(ctx context.Context, request ResumeSessionRequest) (domain.Session, error) {
	if service == nil {
		return domain.Session{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return domain.Session{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	workspaceRoot, err := CanonicalWorkspaceRoot(request.WorkspaceRoot)
	if err != nil {
		return domain.Session{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	state, err := service.LoadSession(ctx, request.SessionID)
	if err != nil {
		return domain.Session{}, err
	}
	storedRoot, err := CanonicalWorkspaceRoot(state.WorkspaceRoot)
	if err != nil {
		return domain.Session{}, storeContractViolation(err)
	}
	if storedRoot != workspaceRoot {
		return domain.Session{}, applicationError(CategoryValidation, "session_not_found", false, nil)
	}
	if err := domain.CheckStartAssistantTurnEligibility(state); err != nil {
		return domain.Session{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	state.WorkspaceRoot = storedRoot
	return state.Clone(), nil
}

func (service *Service) DeleteSession(ctx context.Context, request DeleteSessionRequest) error {
	if service == nil {
		return applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return applicationError(CategoryValidation, "invalid_request", false, err)
	}
	workspaceRoot, err := CanonicalWorkspaceRoot(request.WorkspaceRoot)
	if err != nil {
		return applicationError(CategoryValidation, "invalid_request", false, err)
	}
	state, err := service.loadLifecycleSession(ctx, request.SessionID)
	if err != nil {
		return err
	}
	storedRoot, err := CanonicalWorkspaceRoot(state.WorkspaceRoot)
	if err != nil {
		return storeContractViolation(err)
	}
	if storedRoot != workspaceRoot || state.Status == domain.SessionStatusDeleted {
		return applicationError(CategoryValidation, "session_not_found", false, nil)
	}
	decided, err := domain.Decide(state, domain.DeleteSession{SessionID: request.SessionID})
	if err != nil {
		if domain.IsCode(err, domain.CodeSessionDeleted) {
			return applicationError(CategoryValidation, "session_not_found", false, nil)
		}
		return applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	commandID, sourceErr := service.ids.NewCommandID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return mapped
	}
	if _, err := domain.ParseCommandID(string(commandID)); err != nil {
		return applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	intent, err := BuildAppendIntent(
		service.clock,
		service.ids,
		service.authority.CurrentAuthority(),
		request.SessionID,
		state.Version,
		commandID,
		nil,
		decided,
	)
	if err != nil {
		return err
	}
	_, _, err = CommitAppendIntent(ctx, service.store, state, intent)
	if !isAppendOutcomeUnknown(err) {
		return err
	}
	resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.AppendResolutionTimeout)
	defer cancel()
	receipt, err := ResolveAppendIntent(resolveCtx, service.store, intent, service.appendResolutionConfig())
	if err != nil {
		return err
	}
	_, _, err = ApplyCommittedIntent(state, intent, receipt)
	return err
}

func (service *Service) CloseSession(ctx context.Context, request CloseSessionRequest) (CloseSessionResult, error) {
	if service == nil {
		return CloseSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return CloseSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	state, err := service.LoadSession(ctx, request.SessionID)
	if err != nil {
		return CloseSessionResult{}, err
	}
	decided, err := domain.Decide(state, domain.CloseSession{SessionID: request.SessionID})
	if err != nil {
		return CloseSessionResult{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	commandID, sourceErr := service.ids.NewCommandID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return CloseSessionResult{}, mapped
	}
	if _, err := domain.ParseCommandID(string(commandID)); err != nil {
		return CloseSessionResult{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	next, records, err := appendCompact(ctx, service, request.SessionID, state, decided, commandID, nil)
	if err != nil {
		return CloseSessionResult{}, err
	}
	return CloseSessionResult{Session: next.Clone(), Records: records}, nil
}

func generatedIDError(ctx context.Context, sourceErr error) error {
	if err := contextError(ctx); err != nil {
		if !isNilValue(sourceErr) {
			return applicationError(CategoryCanceled, "canceled", false, errors.Join(ctx.Err(), sourceErr))
		}
		return err
	}
	if !isNilValue(sourceErr) {
		return applicationError(CategoryInternal, "id_generation_failed", false, sourceErr)
	}
	return nil
}

func validRequiredText(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != ""
}
