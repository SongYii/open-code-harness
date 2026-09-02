package eval

import "testing"

func TestNormalizedArgvRejectsInvalidSubject(t *testing.T) {
	subject := validSubject()
	subject.ID = ""
	if _, err := NormalizedArgv(subject); err == nil {
		t.Fatal("NormalizedArgv() error = nil, want a validation refusal for an invalid Subject")
	}
}

func TestNormalizedArgvIncludesInsecureLoopbackOnlyForFixtureLane(t *testing.T) {
	fixture := validSubject()
	fixture.Provider.Lane = ProviderLaneFixture
	argv, err := NormalizedArgv(fixture)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	if !containsArg(argv, "-provider-allow-insecure-loopback") {
		t.Fatalf("argv %v is missing -provider-allow-insecure-loopback for a fixture-lane Subject", argv)
	}

	live := validSubject()
	live.Provider.Lane = ProviderLaneLive
	argv, err = NormalizedArgv(live)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	if containsArg(argv, "-provider-allow-insecure-loopback") {
		t.Fatalf("argv %v unexpectedly includes -provider-allow-insecure-loopback for a live-lane Subject", argv)
	}
}

func TestNormalizedArgvIncludesUnsandboxedFlagOnlyWhenAllowed(t *testing.T) {
	unsandboxed := validSubject()
	unsandboxed.Policy.SandboxPolicy = SandboxPolicyUnsandboxedAllowed
	argv, err := NormalizedArgv(unsandboxed)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	if !containsArg(argv, "-allow-unsandboxed-exec") {
		t.Fatalf("argv %v is missing -allow-unsandboxed-exec", argv)
	}

	sandboxed := validSubject()
	sandboxed.Policy.SandboxPolicy = SandboxPolicySandboxed
	argv, err = NormalizedArgv(sandboxed)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	if containsArg(argv, "-allow-unsandboxed-exec") {
		t.Fatalf("argv %v unexpectedly includes -allow-unsandboxed-exec for a sandboxed Subject", argv)
	}
}

// TestNormalizedArgvCarriesNoAttemptSpecificFlag confirms design's own
// rule that Executor identity's normalized argv carries no Attempt-
// specific path or launch mode: those are a launcher's job to append
// (Task 12), never NormalizedArgv's.
func TestNormalizedArgvCarriesNoAttemptSpecificFlag(t *testing.T) {
	argv, err := NormalizedArgv(validSubject())
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	for _, forbidden := range []string{"-acp", "-workspace", "-database", "-runtime-id", "-audit-dir", "-shutdown-timeout"} {
		if containsArg(argv, forbidden) {
			t.Fatalf("argv %v unexpectedly carries the Attempt/launch-specific flag %q", argv, forbidden)
		}
	}
}

func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}
	return false
}
