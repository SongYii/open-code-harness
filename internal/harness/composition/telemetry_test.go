package composition_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
)

type capturedProviderRequest struct {
	method, path, authorization, contentType, accept string
	body                                             []byte
}

func TestTelemetryDoesNotChangeProviderWire(t *testing.T) {
	const promptCanary = "PROMPT-CONTENT-CANARY-7d6f03"
	const outputCanary = "MODEL-OUTPUT-CANARY-8a44c1"
	var mu sync.Mutex
	var captured []capturedProviderRequest
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		captured = append(captured, capturedProviderRequest{
			method: request.Method, path: request.URL.RequestURI(), authorization: request.Header.Get("Authorization"),
			contentType: request.Header.Get("Content-Type"), accept: request.Header.Get("Accept"), body: append([]byte(nil), body...),
		})
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\""+outputCanary+"\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()

	var otlpPayload bytes.Buffer
	collector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		_, _ = otlpPayload.Write(body)
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	base := validConfig(t)
	base.Provider.BaseURL = provider.URL
	base.Provider.AllowInsecureLoopback = true
	t.Setenv(base.Provider.APIKeyEnv, "wire-test-key")
	run := func(config composition.Config) {
		assembly, err := composition.Open(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		created, err := assembly.Service().CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
		if err == nil {
			_, err = assembly.Service().RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "wire-request", Input: promptCanary, Sink: discardSink{}})
		}
		if err != nil {
			_ = assembly.Close()
			t.Fatal(err)
		}
		if err := assembly.Close(); err != nil {
			t.Fatal(err)
		}
	}

	off := base
	off.DatabasePath = filepath.Join(filepath.Dir(base.DatabasePath), "off.db")
	off.RuntimeID = "wire-off"
	run(off)
	on := base
	on.DatabasePath = filepath.Join(filepath.Dir(base.DatabasePath), "on.db")
	on.RuntimeID = "wire-on"
	on.Telemetry = composition.Telemetry{OTLPTraceEndpoint: collector.URL + "/v1/traces", AllowInsecureLoopback: true}
	run(on)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 || captured[0].method != captured[1].method || captured[0].path != captured[1].path ||
		captured[0].authorization != captured[1].authorization || captured[0].contentType != captured[1].contentType ||
		captured[0].accept != captured[1].accept || !bytes.Equal(captured[0].body, captured[1].body) {
		t.Fatalf("provider wire changed with telemetry: off=%#v on=%#v", captured[0], captured[1])
	}
	if otlpPayload.Len() == 0 {
		t.Fatal("enabled telemetry emitted no OTLP payload on Close")
	}
	for _, forbidden := range []string{promptCanary, outputCanary, "wire-test-key", base.WorkspaceRoot, provider.URL} {
		if bytes.Contains(otlpPayload.Bytes(), []byte(forbidden)) {
			t.Fatalf("raw OTLP payload contained forbidden content %q", forbidden)
		}
	}
}
