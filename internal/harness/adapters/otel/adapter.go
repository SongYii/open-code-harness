package otel

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	QueueSize      = 256
	BatchSize      = 64
	FlushInterval  = time.Second
	ExportTimeout  = 3 * time.Second
	diagnosticRate = time.Minute
)

type Config struct {
	Endpoint              string
	SampleRatio           float64
	AllowInsecureLoopback bool
	ServiceVersion        string
	InstanceID            string
	Diagnostics           io.Writer
}

func ValidateConfig(config Config) error {
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("otel: endpoint must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("otel: endpoint must not contain userinfo, query, or fragment")
	}
	if !strings.HasSuffix(parsed.EscapedPath(), "/v1/traces") {
		return fmt.Errorf("otel: endpoint path must end in /v1/traces")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		ip := net.ParseIP(parsed.Hostname())
		if !config.AllowInsecureLoopback || ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("otel: plaintext endpoint requires an explicitly allowed literal loopback IP")
		}
	default:
		return fmt.Errorf("otel: endpoint scheme must be https or allowed loopback http")
	}
	if config.SampleRatio == 0 {
		config.SampleRatio = 1
	}
	if math.IsNaN(config.SampleRatio) || math.IsInf(config.SampleRatio, 0) || config.SampleRatio <= 0 || config.SampleRatio > 1 {
		return fmt.Errorf("otel: sample ratio must be in (0,1]")
	}
	for name, value := range map[string]string{"service version": config.ServiceVersion, "instance id": config.InstanceID} {
		if value != "" && !telemetry.IsSafeString(value) {
			return fmt.Errorf("otel: %s is not safe bounded metadata", name)
		}
	}
	return nil
}

type Adapter struct {
	provider  *sdktrace.TracerProvider
	tracer    oteltrace.Tracer
	processor *boundedProcessor
}

func New(ctx context.Context, config Config) (*Adapter, error) {
	if ctx == nil {
		return nil, fmt.Errorf("otel: context is required")
	}
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if config.SampleRatio == 0 {
		config.SampleRatio = 1
	}

	transport := &http.Transport{}
	if defaults, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaults.Clone()
	}
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	httpClient := &http.Client{Transport: transport, Timeout: ExportTimeout}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(config.Endpoint),
		otlptracehttp.WithHTTPClient(httpClient),
		otlptracehttp.WithHeaders(map[string]string{}),
		otlptracehttp.WithCompression(otlptracehttp.NoCompression),
		otlptracehttp.WithEncoding(otlptracehttp.EncodingProtobuf),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
		otlptracehttp.WithTimeout(ExportTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: construct OTLP exporter: %w", err)
	}

	attrs := []attribute.KeyValue{attribute.String("service.name", "open-code-harness")}
	if config.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", config.ServiceVersion))
	}
	if config.InstanceID != "" {
		attrs = append(attrs, attribute.String("service.instance.id", config.InstanceID))
	}
	processor := newBoundedProcessor(exporter, config.Diagnostics)
	limits := sdktrace.SpanLimits{
		AttributeValueLengthLimit:   telemetry.MaxValueBytes,
		AttributeCountLimit:         telemetry.MaxAttributes,
		EventCountLimit:             0,
		LinkCountLimit:              0,
		AttributePerEventCountLimit: 0,
		AttributePerLinkCountLimit:  0,
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
		sdktrace.WithResource(resource.NewSchemaless(attrs...)),
		sdktrace.WithRawSpanLimits(limits),
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithoutPanicRecording(),
	)
	return &Adapter{provider: provider, tracer: provider.Tracer("open-code-harness"), processor: processor}, nil
}

func (adapter *Adapter) Start(ctx context.Context, start telemetry.Start) (context.Context, telemetry.Span) {
	if adapter == nil || adapter.tracer == nil || telemetry.ValidateStart(start) != nil {
		return ctx, localNoopSpan{}
	}
	ctx, span := adapter.tracer.Start(ctx, string(start.Kind), oteltrace.WithAttributes(convertAttributes(start.Attributes)...))
	return ctx, &adapterSpan{span: span}
}

func (adapter *Adapter) Shutdown(ctx context.Context) {
	if adapter == nil || adapter.provider == nil || ctx == nil {
		return
	}
	_ = adapter.provider.Shutdown(ctx)
}

func (adapter *Adapter) Dropped() uint64 {
	if adapter == nil || adapter.processor == nil {
		return 0
	}
	return adapter.processor.dropped.Load()
}

type adapterSpan struct {
	once sync.Once
	span oteltrace.Span
}

func (span *adapterSpan) End(end telemetry.End) {
	if span == nil || span.span == nil {
		return
	}
	span.once.Do(func() {
		if telemetry.ValidateEnd(end) != nil {
			span.span.SetAttributes(attribute.String("och.outcome", string(telemetry.OutcomeDropped)))
			span.span.SetStatus(codes.Error, "invalid_telemetry_metadata")
			span.span.End()
			return
		}
		attributes := convertAttributes(end.Attributes)
		attributes = append(attributes, attribute.String("och.outcome", string(end.Outcome)))
		if end.Code != "" {
			attributes = append(attributes, attribute.String("och.error.code", end.Code))
		}
		span.span.SetAttributes(attributes...)
		if end.Outcome == telemetry.OutcomeFailed || end.Outcome == telemetry.OutcomeCanceled || end.Outcome == telemetry.OutcomeTimeout {
			span.span.SetStatus(codes.Error, end.Code)
		}
		span.span.End()
	})
}

type localNoopSpan struct{}

func (localNoopSpan) End(telemetry.End) {}

func convertAttributes(input []telemetry.Attribute) []attribute.KeyValue {
	output := make([]attribute.KeyValue, 0, len(input))
	for _, value := range input {
		name := value.Key.Name()
		switch value.Kind {
		case telemetry.ValueString:
			output = append(output, attribute.String(name, value.String))
		case telemetry.ValueInt64:
			output = append(output, attribute.Int64(name, value.Int64))
		case telemetry.ValueBool:
			output = append(output, attribute.Bool(name, value.Bool))
		}
	}
	return output
}

type flushRequest struct {
	ctx  context.Context
	done chan struct{}
}

type boundedProcessor struct {
	exporter sdktrace.SpanExporter
	queue    chan sdktrace.ReadOnlySpan
	flush    chan flushRequest
	shutdown chan flushRequest
	done     chan struct{}
	closed   atomic.Bool
	dropped  atomic.Uint64
	cancel   context.CancelFunc
	diag     *diagnostics
	once     sync.Once
}

func newBoundedProcessor(exporter sdktrace.SpanExporter, writer io.Writer) *boundedProcessor {
	workCtx, cancel := context.WithCancel(context.Background())
	processor := &boundedProcessor{
		exporter: exporter,
		queue:    make(chan sdktrace.ReadOnlySpan, QueueSize),
		flush:    make(chan flushRequest),
		shutdown: make(chan flushRequest, 1),
		done:     make(chan struct{}),
		cancel:   cancel,
		diag:     newDiagnostics(writer),
	}
	go processor.run(workCtx)
	return processor
}

func (processor *boundedProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (processor *boundedProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if processor == nil || span == nil || processor.closed.Load() {
		return
	}
	select {
	case processor.queue <- span:
	default:
		count := processor.dropped.Add(1)
		processor.diag.write("drop", fmt.Sprintf("telemetry: queue full; spans dropped=%d", count))
	}
}

func (processor *boundedProcessor) ForceFlush(ctx context.Context) error {
	if processor == nil || processor.closed.Load() {
		return nil
	}
	request := flushRequest{ctx: ctx, done: make(chan struct{})}
	select {
	case processor.flush <- request:
	case <-ctx.Done():
		return nil
	}
	select {
	case <-request.done:
	case <-ctx.Done():
	}
	return nil
}

func (processor *boundedProcessor) Shutdown(ctx context.Context) error {
	if processor == nil {
		return nil
	}
	processor.once.Do(func() {
		processor.closed.Store(true)
		processor.cancel()
		processor.shutdown <- flushRequest{ctx: ctx, done: processor.done}
	})
	select {
	case <-processor.done:
	case <-ctx.Done():
	}
	return nil
}

func (processor *boundedProcessor) run(workCtx context.Context) {
	ticker := time.NewTicker(FlushInterval)
	defer ticker.Stop()
	batch := make([]sdktrace.ReadOnlySpan, 0, BatchSize)
	for {
		select {
		case span := <-processor.queue:
			batch = append(batch, span)
			if len(batch) == BatchSize {
				processor.export(workCtx, batch)
				batch = batch[:0]
			}
		case request := <-processor.flush:
			batch = processor.drain(batch)
			processor.export(request.ctx, batch)
			batch = batch[:0]
			close(request.done)
		case request := <-processor.shutdown:
			batch = processor.drain(batch)
			processor.export(request.ctx, batch)
			if err := processor.exporter.Shutdown(request.ctx); err != nil {
				processor.diag.write("shutdown", "telemetry: exporter shutdown failed; spans may be missing")
			}
			close(processor.done)
			return
		case <-ticker.C:
			processor.export(workCtx, batch)
			batch = batch[:0]
		}
	}
}

func (processor *boundedProcessor) drain(batch []sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	for len(batch) < QueueSize+BatchSize {
		select {
		case span := <-processor.queue:
			batch = append(batch, span)
		default:
			return batch
		}
	}
	return batch
}

func (processor *boundedProcessor) export(parent context.Context, spans []sdktrace.ReadOnlySpan) {
	for len(spans) > 0 {
		count := min(len(spans), BatchSize)
		ctx, cancel := context.WithTimeout(parent, ExportTimeout)
		err := processor.exporter.ExportSpans(ctx, spans[:count])
		cancel()
		if err != nil {
			processor.diag.write("export", "telemetry: export failed; spans may be missing")
		}
		spans = spans[count:]
	}
}

type diagnostics struct {
	mu     sync.Mutex
	writer io.Writer
	last   map[string]time.Time
}

func newDiagnostics(writer io.Writer) *diagnostics {
	if writer == nil {
		writer = io.Discard
	}
	return &diagnostics{writer: writer, last: make(map[string]time.Time)}
}

func (diagnostics *diagnostics) write(kind, message string) {
	now := time.Now()
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	if last := diagnostics.last[kind]; !last.IsZero() && now.Sub(last) < diagnosticRate {
		return
	}
	diagnostics.last[kind] = now
	_, _ = fmt.Fprintln(diagnostics.writer, message)
}
