package modeltest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// contractRendezvousTimeout bounds goroutine rendezvous in the contract. It
// fires only when an implementation is genuinely stuck, so it is deliberately
// generous: a tight bound turns CPU contention under -race into a false
// failure rather than a real signal.
const contractRendezvousTimeout = 30 * time.Second

// Contract is the transport-neutral half of the model port: behavior every
// engine.Model must exhibit, whether it is an in-process value or an HTTP
// client.
//
// Error matchers exist because failure identity is not transport-neutral. An
// in-process double returns the sentinel it was handed; an HTTP adapter maps a
// response to a classified provider failure and cannot return someone else's
// error value. The contract therefore asserts that a failure is reported at
// the right moment and is recognizable to the caller, and leaves recognizing
// it to the implementation's own test.
type Contract struct {
	// Factory builds an implementation configured by a Config.
	Factory Factory
	// MatchStartupError reports whether an error returned by Stream is the
	// configured startup failure. Nil requires identity with Config.StartupError.
	MatchStartupError func(error) bool
	// MatchStreamError reports whether an error returned by Next is the
	// configured mid-stream failure. Nil requires identity with the step's Err.
	MatchStreamError func(error) bool
	// MatchCancellation recognizes the adapter's classified cancellation.
	// Nil requires errors.Is(context.Canceled), as for an in-process model.
	MatchCancellation func(error) bool
	// CheckCompletion asserts native completion metadata, without prescribing
	// text chunking. Nil requires absent usage/state for the legacy script.
	CheckCompletion func(*testing.T, engine.StreamEvent, string, []engine.ToolCall)
}

func (contract Contract) matchStartup(configured, got error) bool {
	if contract.MatchStartupError != nil {
		return contract.MatchStartupError(got)
	}
	return errors.Is(got, configured)
}

func (contract Contract) matchStream(configured, got error) bool {
	if contract.MatchStreamError != nil {
		return contract.MatchStreamError(got)
	}
	return errors.Is(got, configured)
}

// RunContract executes the transport-neutral model-port contract. A returned
// stream is consumed by exactly one goroutine; Stream itself is exercised
// concurrently across independent requests.
//
// It deliberately excludes the cases that describe how an in-process Go value
// returns from Stream and Close — a nil stream, a stream returned alongside a
// startup error, and Close accounting. No transport can be asked to produce
// those. They stay in Run, which only in-process implementations call.
func RunContract(t *testing.T, contract Contract) {
	t.Helper()
	factory := contract.Factory

	t.Run("delivers the complete request and ordered unicode events", func(t *testing.T) {
		expected := request("input ✅")
		probe := factory(expected, Config{Steps: []ContractStep{
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "你好"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: " 🌍"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
		}})

		stream, err := probe.Stream(context.Background(), expected)
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer func() {
			if err := stream.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}()

		contract.consume(t, stream, "你好 🌍", nil)
		if expected.Messages != nil || expected.Tools != nil {
			t.Fatalf("default contract request Messages/Tools = (%#v, %#v), want nil", expected.Messages, expected.Tools)
		}
		if gotCalls := probe.Calls(); !reflect.DeepEqual(gotCalls, []engine.ModelRequest{expected}) {
			t.Fatalf("Calls() = %#v, want %#v", gotCalls, []engine.ModelRequest{expected})
		}
	})

	t.Run("delivers ordered assembled tool calls after text", func(t *testing.T) {
		expected := request("tools")
		expected.Tools = []domain.ToolSchema{{Name: "read_file", InputSchema: []byte(`{"type":"object"}`)}}
		calls := []engine.ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"一"}`},
			{ID: "call_2", Name: "read_file", Arguments: `{"path":"二"}`},
		}
		probe := factory(expected, Config{Steps: []ContractStep{
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "reading "}},
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "files"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &calls[0]}},
			{Event: engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &calls[1]}},
			{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
		}})
		stream, err := probe.Stream(context.Background(), expected)
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer func() {
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		}()
		contract.consume(t, stream, "reading files", calls)
		if !reflect.DeepEqual(probe.Calls(), []engine.ModelRequest{expected}) {
			t.Fatal("tool request changed")
		}
	})

	t.Run("returns configured startup error", func(t *testing.T) {
		startup := errors.New("provider unavailable")
		probe := factory(request("startup"), Config{StartupError: startup})
		stream, err := probe.Stream(context.Background(), request("startup"))
		if !contract.matchStartup(startup, err) || stream != nil {
			t.Fatalf("Stream() = (%v, %v), want (nil, startup error)", stream, err)
		}
	})

	t.Run("returns configured mid-stream error", func(t *testing.T) {
		midstream := errors.New("connection lost")
		probe := factory(request("midstream"), Config{Steps: []ContractStep{{Err: midstream}}})
		stream, err := probe.Stream(context.Background(), request("midstream"))
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer stream.Close()
		_, err = stream.Next(context.Background())
		if !contract.matchStream(midstream, err) {
			t.Fatalf("Next() error = %v, want mid-stream error", err)
		}
	})

	// The stream and the Next call share one context, which is how
	// engine.TurnRunner drives the port: it derives streamCtx once and passes
	// it to both Stream and every Next. A transport can only interrupt a read
	// through the context its request was issued with, so cancelling an
	// unrelated context passed to Next is not something the contract may
	// require. See the Slice 5 evidence ledger for the port ambiguity this
	// exposed.
	t.Run("blocks a configured step until cancellation", func(t *testing.T) {
		probe := factory(request("cancel"), Config{Steps: []ContractStep{{WaitForCancel: true}}})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream, err := probe.Stream(ctx, request("cancel"))
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer stream.Close()
		result := make(chan error, 1)
		go func() { _, err := stream.Next(ctx); result <- err }()
		cancel()
		select {
		case err := <-result:
			matches := errors.Is(err, context.Canceled)
			if contract.MatchCancellation != nil {
				matches = contract.MatchCancellation(err)
			}
			if !matches {
				t.Fatalf("Next() error = %v, want context.Canceled", err)
			}
		case <-time.After(contractRendezvousTimeout):
			t.Fatal("Next() did not unblock after cancellation")
		}
	})

	t.Run("records concurrent stream requests independently", func(t *testing.T) {
		first := request("first")
		probe := factory(first, Config{Steps: []ContractStep{
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "ok"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
		}})
		// The scripted implementation deliberately accepts exactly its expected
		// request. Two concurrent calls with that value must still own separate
		// stream cursors; accepting a different route is a native adapter test.
		requests := []engine.ModelRequest{first, first}
		streams := make([]engine.ModelStream, len(requests))
		var wait sync.WaitGroup
		for index, req := range requests {
			wait.Add(1)
			go func(index int, req engine.ModelRequest) {
				defer wait.Done()
				stream, err := probe.Stream(context.Background(), req)
				if err != nil || stream == nil {
					t.Errorf("concurrent Stream() = (%v, %v)", stream, err)
					return
				}
				streams[index] = stream
			}(index, req)
		}
		wait.Wait()
		// Consume on the test goroutine so fatal assertions cannot be lost in
		// a worker. Register all closes before asserting either result.
		for _, stream := range streams {
			if stream != nil {
				defer func() {
					if err := stream.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
		}
		for _, stream := range streams {
			if stream != nil {
				contract.consume(t, stream, "ok", nil)
			}
		}
		calls := probe.Calls()
		if len(calls) != 2 {
			t.Fatalf("len(Calls()) = %d, want 2", len(calls))
		}
		if !reflect.DeepEqual(calls, requests) {
			t.Fatalf("Calls() = %#v, want both independent calls", calls)
		}
	})
}

// consume compares semantic output, not network framing. The finite bound is
// intentionally much larger than these tiny fixtures and catches nonterminal
// streams without making chunk count part of the model contract.
func (contract Contract) consume(t *testing.T, stream engine.ModelStream, wantText string, wantCalls []engine.ToolCall) {
	t.Helper()
	var text strings.Builder
	var calls []engine.ToolCall
	for range 1024 {
		event, err := stream.Next(context.Background())
		if err != nil {
			t.Fatalf("Next before completion: %v", err)
		}
		if event.Type != engine.StreamEventCompleted && (event.Usage != nil || event.ProviderState != nil) {
			t.Fatal("completion metadata before completion")
		}
		switch event.Type {
		case engine.StreamEventTextDelta:
			if len(calls) != 0 || event.ToolCall != nil {
				t.Fatal("text after tools or invalid text event")
			}
			text.WriteString(event.Text)
		case engine.StreamEventToolCall:
			if event.ToolCall == nil || event.Text != "" {
				t.Fatal("invalid tool event")
			}
			calls = append(calls, *event.ToolCall)
		case engine.StreamEventCompleted:
			if event.Text != "" || event.ToolCall != nil {
				t.Fatal("invalid completion shape")
			}
			if text.String() != wantText || !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("output = (%q, %#v), want (%q, %#v)", text.String(), calls, wantText, wantCalls)
			}
			if contract.CheckCompletion != nil {
				contract.CheckCompletion(t, event, text.String(), calls)
			} else if event.Usage != nil || event.ProviderState != nil {
				t.Fatal("unexpected completion metadata")
			}
			return
		default:
			t.Fatalf("unknown event: %q", event.Type)
		}
	}
	t.Fatal("fixture stream did not complete within event bound")
}
