package eval

import (
	"os"
	"testing"
)

func TestBuildChildEnvironmentIncludesOnlyAllowlistedNames(t *testing.T) {
	subject := validSubject()
	subject.Provider.CredentialEnvVar = "OCH_TEST_ACP_CREDENTIAL"
	t.Setenv(subject.Provider.CredentialEnvVar, "the-real-credential-value")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/tester")
	t.Setenv("OCH_EVAL_TEST_UNRELATED_VARIABLE", "must-not-leak-into-the-child")

	env, err := BuildChildEnvironment(subject)
	if err != nil {
		t.Fatalf("BuildChildEnvironment: %v", err)
	}

	names := make(map[string]string, len(env))
	for _, entry := range env {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				names[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	if names["PATH"] != "/usr/bin:/bin" {
		t.Fatalf("PATH = %q, want the forwarded value", names["PATH"])
	}
	if names["HOME"] != "/home/tester" {
		t.Fatalf("HOME = %q, want the forwarded value", names["HOME"])
	}
	if names[subject.Provider.CredentialEnvVar] != "the-real-credential-value" {
		t.Fatalf("credential env var = %q, want the real value", names[subject.Provider.CredentialEnvVar])
	}
	if _, leaked := names["OCH_EVAL_TEST_UNRELATED_VARIABLE"]; leaked {
		t.Fatal("BuildChildEnvironment leaked an unrelated environment variable into the child")
	}
	if len(env) != len(names) {
		t.Fatalf("env has %d entries but only %d distinct names: %v", len(env), len(names), env)
	}
}

func TestBuildChildEnvironmentRequiresCredentialSet(t *testing.T) {
	subject := validSubject()
	subject.Provider.CredentialEnvVar = "OCH_TEST_ACP_CREDENTIAL_UNSET"
	_ = os.Unsetenv(subject.Provider.CredentialEnvVar)

	_, err := BuildChildEnvironment(subject)
	if err == nil {
		t.Fatal("BuildChildEnvironment() error = nil, want a refusal when the credential env var is not set")
	}
}

func TestBuildChildEnvironmentRejectsInvalidSubject(t *testing.T) {
	subject := validSubject()
	subject.ID = ""
	if _, err := BuildChildEnvironment(subject); err == nil {
		t.Fatal("BuildChildEnvironment() error = nil, want a refusal for an invalid Subject")
	}
}
