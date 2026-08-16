package sqlite

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func seedStream(t *testing.T, store *Store, sessionID string, batches int) {
	t.Helper()
	for batch := 0; batch < batches; batch++ {
		request := appendRequest(
			domain.AppendID(appendIDForBatch(batch)),
			domain.SessionID(sessionID),
			uint64(batch*3),
			domain.CommandID(commandIDForBatch(batch)),
			domain.SessionCreated{WorkspaceRoot: "/w"},
			domain.TurnStarted{TurnID: domain.TurnID(turnIDForBatch(batch)), Input: "hi"},
			domain.TurnCompleted{TurnID: domain.TurnID(turnIDForBatch(batch))},
		)
		if batch > 0 {
			request.Events = request.Events[1:]
			request.Events[0].ID = domain.EventID(appendIDForBatch(batch) + "-0")
			request.Events[1].ID = domain.EventID(appendIDForBatch(batch) + "-1")
		}
		mustAppend(t, store, request)
	}
}

func appendIDForBatch(batch int) string {
	if batch == 0 {
		return "append-read-0"
	}
	return string(rune('a'+batch)) + "ppend-read"
}

func commandIDForBatch(batch int) string { return "command-read-" + string(rune('0'+batch)) }
func turnIDForBatch(batch int) string    { return "turn-read-" + string(rune('0'+batch)) }

func TestReadStreamPaginatesAndPinsHead(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	seedStream(t, store, "session-read", 2) // 3 + 2 events, head 5

	ctx := context.Background()
	first, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-read", Limit: 4})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Records) != 4 || first.HeadVersion != 5 || first.NextAfterSequence != 4 || first.End {
		t.Fatalf("first page = %+v", first)
	}
	second, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-read", Limit: 4, AfterSequence: first.NextAfterSequence})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Records) != 1 || second.NextAfterSequence != 5 || !second.End {
		t.Fatalf("second page = %+v", second)
	}

	pinned := uint64(4)
	page, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-read", Limit: 4, HeadVersion: &pinned})
	if err != nil {
		t.Fatalf("pinned page: %v", err)
	}
	if page.HeadVersion != 4 || len(page.Records) != 4 || !page.End {
		t.Fatalf("pinned page = %+v, want head 4 with 4 records at end", page)
	}
	for _, record := range page.Records {
		if record.Sequence > 4 {
			t.Fatalf("pinned record sequence %d beyond head 4", record.Sequence)
		}
	}
}

func TestReadStreamRejectsInvalidCursors(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	seedStream(t, store, "session-cursor", 1)
	ctx := context.Background()

	beyond := uint64(99)
	if _, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-cursor", Limit: 4, HeadVersion: &beyond}); err == nil {
		t.Fatal("pinned head beyond current = nil, want invalid read")
	} else {
		requireStoreCode(t, err, application.StoreCodeInvalidRead)
	}
	if _, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-cursor", Limit: 4, AfterSequence: 9}); err == nil {
		t.Fatal("after-sequence beyond head = nil, want invalid read")
	} else {
		requireStoreCode(t, err, application.StoreCodeInvalidRead)
	}
	if _, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-cursor", Limit: 0}); err == nil {
		t.Fatal("zero limit = nil, want invalid read")
	} else {
		requireStoreCode(t, err, application.StoreCodeInvalidRead)
	}
	if _, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-cursor", Limit: 257}); err == nil {
		t.Fatal("over-limit page = nil, want invalid read")
	} else {
		requireStoreCode(t, err, application.StoreCodeInvalidRead)
	}
}

func TestReadStreamMissingSessionIsEmpty(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-absent", Limit: 10})
	if err != nil {
		t.Fatalf("missing session read: %v", err)
	}
	if len(page.Records) != 0 || page.HeadVersion != 0 || !page.End {
		t.Fatalf("missing session page = %+v", page)
	}
}

func TestResolveAppendOutcomes(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	request := appendRequest("append-resolve", "session-res", 0, "command-res", domain.SessionCreated{WorkspaceRoot: "/w"})
	receipt := mustAppend(t, store, request)

	digest, err := application.DigestAppendRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	resolved, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: "append-resolve", RequestDigest: digest})
	if err != nil {
		t.Fatalf("resolve committed: %v", err)
	}
	if resolved.Kind != application.AppendResolutionCommitted || resolved.Receipt == nil || *resolved.Receipt != receipt {
		t.Fatalf("resolution = %+v, want committed original receipt", resolved)
	}

	absent, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: "append-absent", RequestDigest: digest})
	if err != nil {
		t.Fatalf("resolve absent: %v", err)
	}
	if absent.Kind != application.AppendResolutionNotFound || absent.Receipt != nil {
		t.Fatalf("absent resolution = %+v", absent)
	}

	mismatch, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: "append-resolve", RequestDigest: [32]byte{1}})
	if err != nil {
		t.Fatalf("resolve mismatch: %v", err)
	}
	if mismatch.Kind != application.AppendResolutionIdentityMismatch {
		t.Fatalf("mismatch resolution = %+v", mismatch)
	}
}

func TestFindCommandRequestOutcomes(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	request := admission(appendRequest("append-find", "session-find", 0, "command-find",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-find", Input: "x"},
		domain.AssistantMessageStarted{TurnID: "turn-find", ItemID: "item-find"}),
		"request-find", "turn-find", "item-find")
	mustAppend(t, store, request)
	digest := request.Admission.RequestDigest
	ctx := context.Background()

	found, err := store.FindCommandRequest(ctx, application.FindCommandRequestRequest{RunTurnRequestID: "request-find", SessionID: "session-find", RequestDigest: digest})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Kind != application.CommandRequestLookupFound || found.Record == nil ||
		found.Record.AdmissionAppendID != "append-find" || found.Record.TurnID != "turn-find" ||
		found.Record.ItemID != "item-find" || found.Record.CommandID != "command-find" {
		t.Fatalf("found lookup = %+v", found)
	}

	wrongSession, err := store.FindCommandRequest(ctx, application.FindCommandRequestRequest{RunTurnRequestID: "request-find", SessionID: "session-other", RequestDigest: digest})
	if err != nil {
		t.Fatalf("find wrong session: %v", err)
	}
	if wrongSession.Kind != application.CommandRequestLookupIdentityMismatch || wrongSession.Record != nil {
		t.Fatalf("wrong session lookup = %+v", wrongSession)
	}

	wrongDigest, err := store.FindCommandRequest(ctx, application.FindCommandRequestRequest{RunTurnRequestID: "request-find", SessionID: "session-find", RequestDigest: [32]byte{2}})
	if err != nil {
		t.Fatalf("find wrong digest: %v", err)
	}
	if wrongDigest.Kind != application.CommandRequestLookupIdentityMismatch {
		t.Fatalf("wrong digest lookup = %+v", wrongDigest)
	}

	absent, err := store.FindCommandRequest(ctx, application.FindCommandRequestRequest{RunTurnRequestID: "request-absent", SessionID: "session-find", RequestDigest: digest})
	if err != nil {
		t.Fatalf("find absent: %v", err)
	}
	if absent.Kind != application.CommandRequestLookupNotFound {
		t.Fatalf("absent lookup = %+v", absent)
	}
}

func TestReadersSeeOnlyCompleteBatches(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	mustAppend(t, store, appendRequest("append-race-0", "session-race", 0, "command-race-0", domain.SessionCreated{WorkspaceRoot: "/w"}))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 1; i <= 20; i++ {
			request := appendRequest(
				domain.AppendID(appendIDForRace(i)),
				"session-race",
				uint64(2*i-1),
				domain.CommandID(commandIDForRace(i)),
				domain.TurnStarted{TurnID: domain.TurnID(turnIDForRace(i)), Input: "x"},
				domain.TurnCompleted{TurnID: domain.TurnID(turnIDForRace(i))})
			if _, err := store.Append(ctx, request); err != nil {
				t.Errorf("append %d: %v", i, err)
				return
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			page, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-race", Limit: 256})
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			if uint64(len(page.Records)) != page.HeadVersion {
				t.Errorf("page exposes %d records at head %d; batches are not atomic to readers", len(page.Records), page.HeadVersion)
				return
			}
			runtime.Gosched()
		}
	}()
	wg.Wait()
}

func appendIDForRace(i int) string  { return "append-race-" + string(rune('a'+i)) }
func commandIDForRace(i int) string { return "command-race-" + string(rune('a'+i)) }
func turnIDForRace(i int) string    { return "turn-race-" + string(rune('a'+i)) }
