package testkit

import (
	"context"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// V2Store is a deterministic application-test spy. It deliberately records
// calls and returns only caller-scripted values; it is not an EventStore
// authority and must never be used as a persistence implementation.
type V2Store struct {
	mu          sync.Mutex
	ReadFn      func(context.Context, application.ReadStreamRequest) (application.StreamPage, error)
	AppendFn    func(context.Context, application.AppendRequestV2) (application.CommitReceipt, error)
	ResolveFn   func(context.Context, application.ResolveAppendRequest) (application.AppendResolution, error)
	FindFn      func(context.Context, application.FindCommandRequestRequest) (application.CommandRequestLookup, error)
	Reads       []application.ReadStreamRequest
	Appends     []application.AppendRequestV2
	Resolves    []application.ResolveAppendRequest
	Finds       []application.FindCommandRequestRequest
	ReadPages   []application.StreamPage
	ReadErrs    []error
	Receipts    []application.CommitReceipt
	AppendErrs  []error
	Resolutions []application.AppendResolution
	ResolveErrs []error
	Lookups     []application.CommandRequestLookup
	FindErrs    []error
}

func (store *V2Store) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	store.mu.Lock()
	store.Reads = append(store.Reads, request)
	fn := store.ReadFn
	store.mu.Unlock()
	page := application.StreamPage{End: true}
	var err error
	if fn != nil {
		page, err = fn(ctx, request)
	}
	page = cloneStreamPage(page)
	store.mu.Lock()
	store.ReadPages = append(store.ReadPages, cloneStreamPage(page))
	store.ReadErrs = append(store.ReadErrs, err)
	store.mu.Unlock()
	return page, err
}
func (store *V2Store) Append(ctx context.Context, request application.AppendRequestV2) (application.CommitReceipt, error) {
	store.mu.Lock()
	store.Appends = append(store.Appends, cloneV2Request(request))
	fn := store.AppendFn
	store.mu.Unlock()
	receipt := application.CommitReceipt{}
	var err error
	if fn != nil {
		receipt, err = fn(ctx, cloneV2Request(request))
	}
	store.mu.Lock()
	store.Receipts = append(store.Receipts, receipt)
	store.AppendErrs = append(store.AppendErrs, err)
	store.mu.Unlock()
	return receipt, err
}
func (store *V2Store) ResolveAppend(ctx context.Context, request application.ResolveAppendRequest) (application.AppendResolution, error) {
	store.mu.Lock()
	store.Resolves = append(store.Resolves, request)
	fn := store.ResolveFn
	store.mu.Unlock()
	resolution := application.AppendResolution{Kind: application.AppendResolutionNotFound}
	var err error
	if fn != nil {
		resolution, err = fn(ctx, request)
	}
	resolution = cloneAppendResolution(resolution)
	store.mu.Lock()
	store.Resolutions = append(store.Resolutions, cloneAppendResolution(resolution))
	store.ResolveErrs = append(store.ResolveErrs, err)
	store.mu.Unlock()
	return resolution, err
}
func (store *V2Store) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	store.mu.Lock()
	store.Finds = append(store.Finds, request)
	fn := store.FindFn
	store.mu.Unlock()
	lookup := application.CommandRequestLookup{Kind: application.CommandRequestLookupNotFound}
	var err error
	if fn != nil {
		lookup, err = fn(ctx, request)
	}
	lookup = cloneCommandRequestLookup(lookup)
	store.mu.Lock()
	store.Lookups = append(store.Lookups, cloneCommandRequestLookup(lookup))
	store.FindErrs = append(store.FindErrs, err)
	store.mu.Unlock()
	return lookup, err
}

func cloneV2Request(request application.AppendRequestV2) application.AppendRequestV2 {
	clone := request
	if request.Admission != nil {
		admission := *request.Admission
		clone.Admission = &admission
	}
	clone.Events = make([]application.ProposedEvent, len(request.Events))
	for index, event := range request.Events {
		clone.Events[index] = event
		if copied, err := domain.CloneEvent(event.Event); err == nil {
			clone.Events[index].Event = copied
		}
	}
	return clone
}

func cloneStreamPage(page application.StreamPage) application.StreamPage {
	clone := page
	if records, err := domain.CloneRecordedEvents(page.Records); err == nil {
		clone.Records = records
	} else {
		clone.Records = append([]domain.RecordedEvent(nil), page.Records...)
	}
	return clone
}

func cloneAppendResolution(resolution application.AppendResolution) application.AppendResolution {
	clone := resolution
	if resolution.Receipt != nil {
		receipt := *resolution.Receipt
		clone.Receipt = &receipt
	}
	return clone
}

func cloneCommandRequestLookup(lookup application.CommandRequestLookup) application.CommandRequestLookup {
	clone := lookup
	if lookup.Record != nil {
		record := *lookup.Record
		clone.Record = &record
	}
	return clone
}
