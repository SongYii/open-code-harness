package application

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type CreateSessionRequest struct{ WorkspaceRoot string }
type CreateSessionResult struct {
	SessionID domain.SessionID
	Records   []domain.RecordedEvent
}
type CloseSessionRequest struct{ SessionID domain.SessionID }
type CloseSessionResult struct {
	Session domain.CompactSession
	Records []domain.RecordedEvent
}

func (service *Service) CreateSession(ctx context.Context, request CreateSessionRequest) (CreateSessionResult, error) {
	if service == nil || !validRequiredText(request.WorkspaceRoot) {
		return CreateSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
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
	decided, err := domain.DecideCompact(domain.CompactSession{}, domain.CreateSession{SessionID: sessionID, WorkspaceRoot: request.WorkspaceRoot})
	if err != nil {
		return CreateSessionResult{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	next, records, err := appendCompact(ctx, service, sessionID, domain.CompactSession{}, decided, commandID, nil)
	if err != nil {
		return CreateSessionResult{}, err
	}
	if next.ID != sessionID {
		return CreateSessionResult{}, storeContractViolation(nil)
	}
	return CreateSessionResult{SessionID: sessionID, Records: records}, nil
}

func (service *Service) LoadSession(ctx context.Context, sessionID domain.SessionID) (domain.CompactSession, error) {
	if service == nil {
		return domain.CompactSession{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	return loadCompactSessionPinned(ctx, service.store, sessionID)
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
	decided, err := domain.DecideCompact(state, domain.CloseSession{SessionID: request.SessionID})
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
