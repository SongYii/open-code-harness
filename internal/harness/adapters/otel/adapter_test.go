package otel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestValidateConfig(t *testing.T) {
	valid := Config{Endpoint: "https://collector.example/v1/traces"}
	if err := ValidateConfig(valid); err != nil {
		t.Fatalf("ValidateConfig(valid) = %v", err)
	}
	for _, test := range []Config{
		{},
		{Endpoint: "https://collector.example"},
		{Endpoint: "https://user:password@collector.example/v1/traces"},
		{Endpoint: "https://collector.example/v1/traces?key=secret"},
		{Endpoint: "http://collector.example/v1/traces", AllowInsecureLoopback: true},
		{Endpoint: "http://localhost:4318/v1/traces", AllowInsecureLoopback: true},
		{Endpoint: "http://127.0.0.1:4318/v1/traces"},
		{Endpoint: "http://127.0.0.1:4318/v1/traces", AllowInsecureLoopback: true, SampleRatio: 2},
		{Endpoint: "https://collector.example/v1/traces", ServiceVersion: "contains spaces"},
	} {
		if err := ValidateConfig(test); err == nil {
			t.Fatalf("ValidateConfig(%#v) succeeded", test)
		}
	}
}

func TestAdapterExportsParentedMetadataOnlyOTLP(t *testing.T) {
	var mu sync.Mutex
	var payloads [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		payloads = append(payloads, append([]byte(nil), body...))
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter, err := New(context.Background(), Config{
		Endpoint: server.URL + "/v1/traces", AllowInsecureLoopback: true,
		ServiceVersion: "v-test", InstanceID: "runtime-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	canary := "PROMPT CANARY secret=sk-should-never-leave"
	rootCtx, root := telemetry.SafeStart(adapter, context.Background(), telemetry.Start{
		Kind: telemetry.KindTurn, Attributes: []telemetry.Attribute{
			telemetry.String(telemetry.KeySessionID, "session-1"),
		},
	})
	// This entire invalid record is dropped at the project port: arbitrary
	// content cannot be smuggled through an otherwise legal string key.
	_, rejected := telemetry.SafeStart(adapter, rootCtx, telemetry.Start{
		Kind:       telemetry.KindToolExecute,
		Attributes: []telemetry.Attribute{telemetry.String(telemetry.KeyToolName, canary)},
	})
	rejected.End(telemetry.End{Outcome: telemetry.OutcomeOK})
	_, child := telemetry.SafeStart(adapter, rootCtx, telemetry.Start{
		Kind: telemetry.KindModelRequest,
		Attributes: []telemetry.Attribute{
			telemetry.String(telemetry.KeyModelPurpose, "conversation"),
			telemetry.Uint64(telemetry.KeyUsageInputTokens, 12),
		},
	})
	child.End(telemetry.End{Outcome: telemetry.OutcomeOK, Attributes: []telemetry.Attribute{telemetry.Uint64(telemetry.KeyUsageOutputTokens, 3)}})
	root.End(telemetry.End{Outcome: telemetry.OutcomeOK})
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	adapter.Shutdown(shutdownCtx)

	mu.Lock()
	joined := bytes.Join(payloads, nil)
	mu.Unlock()
	if len(joined) == 0 {
		t.Fatal("no OTLP payload received")
	}
	if bytes.Contains(joined, []byte(canary)) {
		t.Fatal("forbidden content canary appeared in raw OTLP")
	}
	request := &collectortrace.ExportTraceServiceRequest{}
	if err := proto.Unmarshal(joined, request); err != nil {
		t.Fatalf("decode OTLP: %v", err)
	}
	spans := request.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 2 {
		t.Fatalf("span count = %d, want 2", len(spans))
	}
	byName := map[string]struct{ traceID, spanID, parent []byte }{}
	for _, span := range spans {
		byName[span.Name] = struct{ traceID, spanID, parent []byte }{span.TraceId, span.SpanId, span.ParentSpanId}
	}
	rootWire := byName[string(telemetry.KindTurn)]
	childWire := byName[string(telemetry.KindModelRequest)]
	if len(rootWire.parent) != 0 || !bytes.Equal(rootWire.traceID, childWire.traceID) || !bytes.Equal(rootWire.spanID, childWire.parent) {
		t.Fatalf("invalid parentage: root=%#v child=%#v", rootWire, childWire)
	}
}

func TestBoundedProcessorDropsWithoutBlockingAndHidesExporterError(t *testing.T) {
	exporter := &blockingExporter{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("https://secret.invalid/?token=sk-leak")}
	var diagnostics bytes.Buffer
	processor := newBoundedProcessor(exporter, &diagnostics)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(processor))
	tracer := provider.Tracer("test")

	for index := 0; index < BatchSize; index++ {
		_, span := tracer.Start(context.Background(), "fill-batch")
		span.End()
	}
	select {
	case <-exporter.entered:
	case <-time.After(time.Second):
		t.Fatal("processor did not begin export")
	}
	started := time.Now()
	for index := 0; index < QueueSize*3; index++ {
		_, span := tracer.Start(context.Background(), "overflow")
		span.End()
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("OnEnd blocked for %s", elapsed)
	}
	if processor.dropped.Load() == 0 {
		t.Fatal("queue overflow did not record drops")
	}
	close(exporter.release)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
	text := diagnostics.String()
	if !strings.Contains(text, "spans dropped=") || !strings.Contains(text, "export failed") {
		t.Fatalf("diagnostics = %q", text)
	}
	if strings.Contains(text, "secret.invalid") || strings.Contains(text, "sk-leak") {
		t.Fatalf("diagnostics leaked raw exporter error: %q", text)
	}
}

type blockingExporter struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	err     error
}

func (exporter *blockingExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	exporter.once.Do(func() { close(exporter.entered) })
	select {
	case <-exporter.release:
		return exporter.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (exporter *blockingExporter) Shutdown(context.Context) error { return nil }
