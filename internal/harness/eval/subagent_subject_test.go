package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSubjectSubagentsRoundTripAndLegacyOmission(t *testing.T) {
	legacy := validSubject()
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("json.Marshal(legacy): %v", err)
	}
	if strings.Contains(string(legacyJSON), "subagents") {
		t.Fatalf("disabled legacy Subject gained subagents bytes: %s", legacyJSON)
	}

	want := validSubject()
	want.Subagents = &SubjectSubagents{Enabled: true, Timeout: 45 * time.Second}
	got, err := DecodeSubject(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeSubject: %v", err)
	}
	if got.Subagents == nil || !got.Subagents.Enabled || got.Subagents.Timeout != 45*time.Second {
		t.Fatalf("Subagents = %#v", got.Subagents)
	}
}

func TestSubjectSubagentsRequireExplicitEnabledBoundedTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  SubjectSubagents
	}{
		{"disabled", SubjectSubagents{Timeout: 45 * time.Second}},
		{"implicit timeout", SubjectSubagents{Enabled: true}},
		{"below minimum", SubjectSubagents{Enabled: true, Timeout: 4 * time.Second}},
		{"above maximum", SubjectSubagents{Enabled: true, Timeout: 11 * time.Minute}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject := validSubject()
			subject.Subagents = &test.cfg
			if err := subject.Validate(); err == nil {
				t.Fatalf("Validate() accepted %#v", test.cfg)
			}
		})
	}
}

func TestBuildConfigMapsFrozenSubagents(t *testing.T) {
	subject := validSubject()
	subject.Subagents = &SubjectSubagents{Enabled: true, Timeout: 45 * time.Second}
	config, err := BuildConfig(subject, testDirectories(t, testAttemptID(t)), "runtime-1", nil)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if !config.Subagents.Enabled || config.Subagents.Timeout != 45*time.Second {
		t.Fatalf("Subagents = %#v", config.Subagents)
	}
}

func TestNormalizedArgvCarriesFrozenSubagents(t *testing.T) {
	subject := validSubject()
	subject.Subagents = &SubjectSubagents{Enabled: true, Timeout: 45 * time.Second}
	argv, err := NormalizedArgv(subject)
	if err != nil {
		t.Fatalf("NormalizedArgv: %v", err)
	}
	want := []string{"-subagents", "-subagent-timeout", "45s"}
	for _, arg := range want {
		if !containsArg(argv, arg) {
			t.Fatalf("argv %v does not contain %q", argv, arg)
		}
	}
}
