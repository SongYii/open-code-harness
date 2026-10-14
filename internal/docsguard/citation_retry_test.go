package docsguard_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCitationRetriesTransientResponsesWithinOneBudget(t *testing.T) {
	for _, status := range []int{429, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) < 3 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			got := probeCitationContext(t.Context(), server.URL, 0)
			if got.err != nil || got.status != 200 || calls.Load() != 3 {
				t.Fatalf("result=%+v calls=%d", got, calls.Load())
			}
		})
	}
}

func TestCitationDoesNotExcuseDeadOrPersistentLinks(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(status) }))
			defer server.Close()
			got := probeCitationContext(t.Context(), server.URL, 0)
			wantCalls := int32(1)
			if status == 503 {
				wantCalls = 3
			}
			if got.err != nil || got.status != status || calls.Load() != wantCalls {
				t.Fatalf("result=%+v calls=%d", got, calls.Load())
			}
		})
	}
}

func TestCitationRetryWaitHonorsSharedDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := probeCitationContext(ctx, server.URL, time.Minute)
	if !errors.Is(got.err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("result=%+v elapsed=%s", got, time.Since(started))
	}
}
