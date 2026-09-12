package application_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	oteladapter "github.com/SongYii/open-code-harness/internal/harness/adapters/otel"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func BenchmarkRunTurnTelemetry(b *testing.B) {
	for _, test := range []struct {
		name  string
		block bool
	}{{"disabled", false}, {"sampled_otel", false}, {"forced_drop_otel", true}} {
		b.Run(test.name, func(b *testing.B) {
			store, err := memory.NewEventStore(v2Authority)
			if err != nil {
				b.Fatal(err)
			}
			runner, err := engine.NewTurnRunner(&acceptanceSuccessModel{text: "ok"})
			if err != nil {
				b.Fatal(err)
			}
			config := application.DefaultConfig()
			if test.name != "disabled" {
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if test.block {
						select {
						case <-release:
						case <-request.Context().Done():
						}
						return
					}
					_, _ = io.Copy(io.Discard, request.Body)
					writer.WriteHeader(http.StatusOK)
				}))
				defer server.Close()
				adapter, adapterErr := oteladapter.New(context.Background(), oteladapter.Config{Endpoint: server.URL + "/v1/traces", AllowInsecureLoopback: true})
				if adapterErr != nil {
					b.Fatal(adapterErr)
				}
				defer func() {
					close(release)
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
					defer cancel()
					adapter.Shutdown(ctx)
				}()
				config.Telemetry = adapter
			}
			service, err := application.NewService(store, testkit.NewSequenceIDs(), testkit.FixedClock{Time: time.Unix(1, 0)}, runner, v2Authority, config)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				created, createErr := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
				if createErr != nil {
					b.Fatal(createErr)
				}
				_, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("benchmark-%d", index)), Input: "hello", Sink: &testkit.RecordingSink{}})
				if runErr != nil {
					b.Fatal(runErr)
				}
			}
		})
	}
}
