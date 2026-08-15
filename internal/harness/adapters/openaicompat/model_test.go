package openaicompat

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "empty base url", mutate: func(cfg *Config) { cfg.BaseURL = "" }},
		{name: "empty model id", mutate: func(cfg *Config) { cfg.ModelID = "" }},
		{name: "nil api key", mutate: func(cfg *Config) { cfg.APIKey = nil }},
		{name: "userinfo", mutate: func(cfg *Config) { cfg.BaseURL = "https://user:pass@api.example.com/v1" }},
		{name: "http non-loopback", mutate: func(cfg *Config) { cfg.BaseURL = "http://api.example.com/v1" }},
		{name: "http metadata even with flag", mutate: func(cfg *Config) {
			cfg.BaseURL = "http://169.254.169.254/latest"
			cfg.AllowInsecureLoopback = true
		}},
		{name: "http non-loopback with flag", mutate: func(cfg *Config) {
			cfg.BaseURL = "http://example.com/v1"
			cfg.AllowInsecureLoopback = true
		}},
		{name: "native tools required", mutate: func(cfg *Config) { cfg.Profile.NativeTools = engine.CapabilityRequired }},
		{name: "empty profile tri-state", mutate: func(cfg *Config) { cfg.Profile.Images = "" }},
		{name: "invalid max tokens field", mutate: func(cfg *Config) { cfg.Hints.MaxTokensField = "tokens" }},
		{name: "negative idle", mutate: func(cfg *Config) { cfg.IdleTimeout = -1 }},
		{name: "ftp scheme", mutate: func(cfg *Config) { cfg.BaseURL = "ftp://api.example.com/v1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig(&scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				t.Fatal("New must not perform network I/O")
				return nil, nil
			}})
			test.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("New() error = nil, want invalid config")
			}
		})
	}
}

func TestNewAcceptsLoopbackHTTPWhenAllowed(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:8080/v1", "http://localhost/v1", "http://[::1]/v1"} {
		cfg := validConfig(nil)
		cfg.HTTPClient = nil
		cfg.BaseURL = base
		cfg.AllowInsecureLoopback = true
		model, err := New(cfg)
		if err != nil {
			t.Fatalf("New(%q) error = %v", base, err)
		}
		if model.Identity().EndpointID == "" {
			t.Fatalf("Identity().EndpointID empty for %q", base)
		}
	}
}

func TestIdentityCopiesProfileAndHints(t *testing.T) {
	cfg := validConfig(nil)
	cfg.HTTPClient = nil
	cfg.Hints = WireHints{IncludeUsage: true, MaxTokensField: "max_completion_tokens"}
	cfg.Profile = ProfileTextOnly(128000, 4096)
	model := newTestModel(t, cfg)
	got := model.Identity()
	if err := got.Validate(); err != nil {
		t.Fatalf("Identity().Validate() = %v", err)
	}
	if got.AdapterFamily != adapterFamily || got.ModelID != "test-model" || got.EndpointID != "api.example.com/v1" {
		t.Fatalf("Identity() = %#v", got)
	}
	if !got.IncludeUsage || got.MaxTokensField != "max_completion_tokens" {
		t.Fatalf("Identity hints = include=%t field=%q", got.IncludeUsage, got.MaxTokensField)
	}
	if got.Profile != cfg.Profile {
		t.Fatalf("Identity profile = %#v, want %#v", got.Profile, cfg.Profile)
	}
}

func TestNewDoesNotMutateDefaultClientOrTransport(t *testing.T) {
	origCheck := http.DefaultClient.CheckRedirect
	origTimeout := http.DefaultClient.Timeout
	origClientTransport := http.DefaultClient.Transport
	defaultTransport, _ := http.DefaultTransport.(*http.Transport)
	var origHeaderTimeout time.Duration
	if defaultTransport != nil {
		origHeaderTimeout = defaultTransport.ResponseHeaderTimeout
	}
	t.Cleanup(func() {
		http.DefaultClient.CheckRedirect = origCheck
		http.DefaultClient.Timeout = origTimeout
		http.DefaultClient.Transport = origClientTransport
		if defaultTransport != nil {
			defaultTransport.ResponseHeaderTimeout = origHeaderTimeout
		}
	})

	injected := &http.Transport{ResponseHeaderTimeout: 3 * time.Second}
	client := &http.Client{Timeout: 9 * time.Second, Transport: injected, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	cfg := validConfig(injected)
	cfg.HTTPClient = client
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}
	if client.CheckRedirect == nil || client.Timeout != 9*time.Second || client.Transport != injected {
		t.Fatalf("injected client mutated: %#v", client)
	}
	if injected.ResponseHeaderTimeout != 3*time.Second {
		t.Fatalf("injected transport mutated: %v", injected.ResponseHeaderTimeout)
	}
	if sameFunc(http.DefaultClient.CheckRedirect, origCheck) == false || http.DefaultClient.Timeout != origTimeout || http.DefaultClient.Transport != origClientTransport {
		t.Fatal("http.DefaultClient mutated")
	}
	if defaultTransport != nil && defaultTransport.ResponseHeaderTimeout != origHeaderTimeout {
		t.Fatal("http.DefaultTransport mutated")
	}

	cfg.HTTPClient = nil
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}
	if sameFunc(http.DefaultClient.CheckRedirect, origCheck) == false || defaultTransport != nil && defaultTransport.ResponseHeaderTimeout != origHeaderTimeout {
		t.Fatal("nil-client New mutated process HTTP defaults")
	}
}

func sameFunc(a, b any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func TestStreamRequestMapping(t *testing.T) {
	tests := []struct {
		name          string
		hints         WireHints
		maxOutput     uint32
		wantOptions   bool
		wantMaxField  string
		wantMaxAbsent bool
	}{
		{name: "include usage", hints: WireHints{IncludeUsage: true}, wantOptions: true, wantMaxAbsent: true},
		{name: "omit usage", hints: WireHints{}, wantOptions: false, wantMaxAbsent: true},
		{name: "max tokens", hints: WireHints{MaxTokensField: "max_tokens"}, maxOutput: 32, wantMaxField: "max_tokens"},
		{name: "max completion tokens", hints: WireHints{MaxTokensField: "max_completion_tokens"}, maxOutput: 16, wantMaxField: "max_completion_tokens"},
		{name: "omit max when tokens zero", hints: WireHints{MaxTokensField: "max_tokens"}, wantMaxAbsent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var seen *http.Request
			transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
				seen = req
				return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
			}}
			cfg := validConfig(transport)
			cfg.Hints = test.hints
			cfg.Profile.MaxOutputTokens = test.maxOutput
			model := newTestModel(t, cfg)
			stream, err := model.Stream(context.Background(), modelRequest())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			if seen == nil {
				t.Fatal("no request")
			}
			if seen.Method != http.MethodPost || !strings.HasSuffix(seen.URL.String(), "/v1/chat/completions") {
				t.Fatalf("request = %s %s", seen.Method, seen.URL)
			}
			if seen.Header.Get("User-Agent") != defaultUserAgent || seen.Header.Get("Accept") != "text/event-stream" || seen.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("headers = %v", seen.Header)
			}
			payload := decodeRequestBody(t, seen)
			if payload["model"] != "test-model" || payload["stream"] != true {
				t.Fatalf("payload = %#v", payload)
			}
			_, hasOptions := payload["stream_options"]
			if hasOptions != test.wantOptions {
				t.Fatalf("stream_options present=%t, want %t", hasOptions, test.wantOptions)
			}
			if test.wantMaxField != "" {
				if _, ok := payload[test.wantMaxField]; !ok {
					t.Fatalf("missing %s in %#v", test.wantMaxField, payload)
				}
			}
			if test.wantMaxAbsent {
				if _, ok := payload["max_tokens"]; ok {
					t.Fatalf("max_tokens unexpectedly present")
				}
				if _, ok := payload["max_completion_tokens"]; ok {
					t.Fatalf("max_completion_tokens unexpectedly present")
				}
			}
		})
	}
}

func TestStreamMissingAPIKeyIsAuth(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		t.Fatal("missing key must not send HTTP")
		return nil, nil
	}}
	cfg := validConfig(transport)
	cfg.APIKey = StaticAPIKey{Value: "  "}
	model := newTestModel(t, cfg)
	_, err := model.Stream(context.Background(), modelRequest())
	requireProviderFailure(t, err, engine.CodeModelStartup, "provider_auth")
}

func TestEnvAPIKeyIsReadAtStreamTime(t *testing.T) {
	const envName = "OPEN_CODE_HARNESS_TEST_KEY_3529B50B"
	t.Setenv(envName, "")
	var gotAuth string
	transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
	}}
	cfg := validConfig(transport)
	cfg.APIKey = EnvAPIKey{Name: envName}
	model := newTestModel(t, cfg)
	if _, err := model.Stream(context.Background(), modelRequest()); err == nil {
		t.Fatal("empty env key should fail at Stream")
	}
	t.Setenv(envName, "rotated-key")
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if gotAuth != "Bearer rotated-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestStreamRejectsOversizeRequest(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		t.Fatal("oversize JSON must not be sent")
		return nil, nil
	}}
	cfg := validConfig(transport)
	cfg.MaxRequestBytes = 32
	model := newTestModel(t, cfg)
	req := modelRequest()
	req.Input = strings.Repeat("x", 64)
	_, err := model.Stream(context.Background(), req)
	requireProviderFailure(t, err, engine.CodeModelStartup, "provider_permanent")
}

func TestStreamCanceledContextDoesNotSend(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		t.Fatal("canceled context must not send HTTP")
		return nil, nil
	}}
	model := newTestModel(t, validConfig(transport))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := model.Stream(ctx, modelRequest())
	if !engine.IsCode(err, engine.CodeCanceled) {
		t.Fatalf("Stream() error = %v, want canceled", err)
	}
}
