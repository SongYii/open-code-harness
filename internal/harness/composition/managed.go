package composition

import (
	"context"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
)

// Service is the lifecycle-managed use-case boundary. The raw application
// service stays private so internal use-case calls never reenter admission.
type Service interface {
	CreateSession(context.Context, application.CreateSessionRequest) (application.CreateSessionResult, error)
	LoadSession(context.Context, domain.SessionID) (domain.Session, error)
	ResumeSession(context.Context, application.ResumeSessionRequest) (domain.Session, error)
	ListSessions(context.Context, application.ListSessionsRequest) (application.ListSessionsResult, error)
	CloseSession(context.Context, application.CloseSessionRequest) (application.CloseSessionResult, error)
	RunTurn(context.Context, application.RunTurnRequest) (application.RunTurnResult, error)
	CompactSession(context.Context, application.CompactSessionRequest) (application.CompactSessionResult, error)
	DeleteSession(context.Context, application.DeleteSessionRequest) error
}
type managedService struct {
	host  *runtime.Host
	inner *application.Service
}

func admitted[T any](host *runtime.Host, ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	work, finish, err := host.Admit(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	defer finish()
	return call(work)
}
func (service *managedService) CreateSession(ctx context.Context, request application.CreateSessionRequest) (application.CreateSessionResult, error) {
	return admitted(service.host, ctx, func(work context.Context) (application.CreateSessionResult, error) {
		return service.inner.CreateSession(work, request)
	})
}
func (service *managedService) LoadSession(ctx context.Context, request domain.SessionID) (domain.Session, error) {
	return admitted(service.host, ctx, func(work context.Context) (domain.Session, error) { return service.inner.LoadSession(work, request) })
}
func (service *managedService) ResumeSession(ctx context.Context, request application.ResumeSessionRequest) (domain.Session, error) {
	return admitted(service.host, ctx, func(work context.Context) (domain.Session, error) { return service.inner.ResumeSession(work, request) })
}
func (service *managedService) ListSessions(ctx context.Context, request application.ListSessionsRequest) (application.ListSessionsResult, error) {
	return admitted(service.host, ctx, func(work context.Context) (application.ListSessionsResult, error) {
		return service.inner.ListSessions(work, request)
	})
}
func (service *managedService) CloseSession(ctx context.Context, request application.CloseSessionRequest) (application.CloseSessionResult, error) {
	return admitted(service.host, ctx, func(work context.Context) (application.CloseSessionResult, error) {
		return service.inner.CloseSession(work, request)
	})
}
func (service *managedService) RunTurn(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
	return admitted(service.host, ctx, func(work context.Context) (application.RunTurnResult, error) {
		return service.inner.RunTurn(work, request)
	})
}
func (service *managedService) CompactSession(ctx context.Context, request application.CompactSessionRequest) (application.CompactSessionResult, error) {
	return admitted(service.host, ctx, func(work context.Context) (application.CompactSessionResult, error) {
		return service.inner.CompactSession(work, request)
	})
}
func (service *managedService) DeleteSession(ctx context.Context, request application.DeleteSessionRequest) error {
	_, err := admitted(service.host, ctx, func(work context.Context) (struct{}, error) {
		return struct{}{}, service.inner.DeleteSession(work, request)
	})
	return err
}

// managedStore protects externally requested reads/writes from concurrent
// teardown. The application itself uses the raw store for cleanup after
// admission closes; admitting each internal append would break that guarantee.
type managedStore struct {
	host  *runtime.Host
	inner application.EventStore
}

func (store *managedStore) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	return admitted(store.host, ctx, func(work context.Context) (application.StreamPage, error) {
		return store.inner.ReadStream(work, request)
	})
}
func (store *managedStore) ListSessionHeads(ctx context.Context, request application.ListSessionHeadsRequest) (application.SessionHeadPage, error) {
	return admitted(store.host, ctx, func(work context.Context) (application.SessionHeadPage, error) {
		return store.inner.ListSessionHeads(work, request)
	})
}
func (store *managedStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	return admitted(store.host, ctx, func(work context.Context) (application.CommitReceipt, error) {
		return store.inner.Append(work, request)
	})
}
func (store *managedStore) ResolveAppend(ctx context.Context, request application.ResolveAppendRequest) (application.AppendResolution, error) {
	return admitted(store.host, ctx, func(work context.Context) (application.AppendResolution, error) {
		return store.inner.ResolveAppend(work, request)
	})
}
func (store *managedStore) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	return admitted(store.host, ctx, func(work context.Context) (application.CommandRequestLookup, error) {
		return store.inner.FindCommandRequest(work, request)
	})
}
