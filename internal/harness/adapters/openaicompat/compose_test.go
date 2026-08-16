package openaicompat_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

var (
	fixtureTime      = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	fixtureAuthority = application.WriterAuthority{RuntimeID: "openaicompat-runtime", FencingToken: 1}
)

// MustComposeHTTP builds a Service on the supplied EventStore with the HTTP
// adapter's Identity copied into Config.RequestIdentity. Identity must be
// non-zero. AllowInsecureLoopback is set only for loopback fixture servers.
func MustComposeHTTP(t *testing.T, store application.EventStore, cfg openaicompat.Config) (*application.Service, *openaicompat.Model) {
	t.Helper()
	if store == nil {
		t.Fatal("MustComposeHTTP: store is required")
	}
	cfg.AllowInsecureLoopback = allowInsecureLoopback(cfg.BaseURL)
	model, err := openaicompat.New(cfg)
	if err != nil {
		t.Fatalf("openaicompat.New() error = %v", err)
	}
	identity := model.Identity()
	if identity == (engine.RequestIdentity{}) {
		t.Fatal("MustComposeHTTP: adapter identity is zero")
	}
	if err := identity.Validate(); err != nil {
		t.Fatalf("MustComposeHTTP: adapter identity invalid: %v", err)
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	appCfg := application.DefaultConfig()
	copied := identity
	appCfg.RequestIdentity = &copied
	service, err := application.NewService(store, testkit.NewSequenceIDs(), testkit.FixedClock{Time: fixtureTime}, runner, fixtureAuthority, appCfg)
	if err != nil {
		t.Fatal(err)
	}
	return service, model
}

func MustComposeHTTPTools(t *testing.T, store application.EventStore, cfg openaicompat.Config, files tools.FileSystem) (*application.Service, *openaicompat.Model) {
	t.Helper()
	if store == nil || files == nil {
		t.Fatal("MustComposeHTTPTools: store and files are required")
	}
	cfg.AllowInsecureLoopback = allowInsecureLoopback(cfg.BaseURL)
	model, err := openaicompat.New(cfg)
	if err != nil {
		t.Fatalf("openaicompat.New() error = %v", err)
	}
	identity := model.Identity()
	if identity == (engine.RequestIdentity{}) {
		t.Fatal("MustComposeHTTPTools: adapter identity is zero")
	}
	if err := identity.Validate(); err != nil {
		t.Fatalf("MustComposeHTTPTools: adapter identity invalid: %v", err)
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	catalog := readFileCatalog(t)
	appCfg := application.DefaultConfig()
	copied := identity
	appCfg.RequestIdentity = &copied
	appCfg.Catalog = catalog
	appCfg.Files = files
	service, err := application.NewService(store, testkit.NewSequenceIDs(), testkit.FixedClock{Time: fixtureTime}, runner, fixtureAuthority, appCfg)
	if err != nil {
		t.Fatal(err)
	}
	return service, model
}

func readFileCatalog(t *testing.T) *tools.Catalog {
	t.Helper()
	var specs []domain.ToolSpec
	for _, spec := range tools.DefaultWorkspaceSpecs() {
		if spec.Name == tools.NameReadFile {
			specs = append(specs, spec)
		}
	}
	catalog, err := tools.NewCatalog(specs)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func fixtureToolsConfig(rt http.RoundTripper) openaicompat.Config {
	cfg := fixtureConfig(rt)
	cfg.Profile = openaicompat.ProfileToolsSupported(8192, 4096)
	cfg.MaxRequestBytes = 5 << 20
	return cfg
}

func TestMustComposeHTTPSetsLoopbackForFixtureServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "loopback-1")
		_, _ = io.WriteString(w, loadSSE(t, "success.sse"))
	}))
	t.Cleanup(server.Close)

	store := newMemoryStore(t)
	cfg := fixtureConfig(nil)
	cfg.HTTPClient = server.Client()
	cfg.BaseURL = server.URL + "/v1"
	cfg.AllowInsecureLoopback = false
	service, model := MustComposeHTTP(t, store, cfg)
	if model.Identity().EndpointID == "" || model.Identity().AdapterFamily == "" {
		t.Fatalf("Identity() = %#v, want non-zero", model.Identity())
	}

	sessionID := createSession(t, service)
	result, err := runTurn(t, service, sessionID, "request-loopback", "inspect")
	if err != nil || result.Status != domain.TurnStatusCompleted || result.Text != "Hello world" || !result.TerminalCommitted {
		t.Fatalf("loopback RunTurn() result = %#v, err = %v", result, err)
	}
}

func TestMustComposeHTTPRejectsNonLoopbackHTTP(t *testing.T) {
	cfg := fixtureConfig(&countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		t.Fatal("non-loopback HTTP must not send")
		return nil, nil
	}})
	cfg.BaseURL = "http://example.com/v1"
	cfg.AllowInsecureLoopback = true
	cfg.AllowInsecureLoopback = allowInsecureLoopback(cfg.BaseURL)
	if cfg.AllowInsecureLoopback {
		t.Fatal("AllowInsecureLoopback set for non-loopback host")
	}
	if _, err := openaicompat.New(cfg); err == nil {
		t.Fatal("New() accepted non-loopback http after composition policy")
	}
}

func newMemoryStore(t *testing.T) *memory.EventStore {
	t.Helper()
	store, err := memory.NewEventStore(fixtureAuthority)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func fixtureConfig(rt http.RoundTripper) openaicompat.Config {
	return openaicompat.Config{
		BaseURL: "https://api.example.com/v1",
		ModelID: "test-model",
		APIKey:  openaicompat.StaticAPIKey{Value: "test-key"},
		Profile: openaicompat.ProfileTextOnly(8192, 4096),
		Hints:   openaicompat.WireHints{IncludeUsage: true, MaxTokensField: "max_tokens"},
		HTTPClient: &http.Client{
			Transport: rt,
		},
	}
}

func allowInsecureLoopback(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func createSession(t *testing.T, service *application.Service) domain.SessionID {
	t.Helper()
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	return created.SessionID
}
