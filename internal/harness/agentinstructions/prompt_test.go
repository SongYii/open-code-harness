package agentinstructions

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const goldenPromptDigest = "sha256:8060287d0ddc132ebce66e955b8749804a06d1d1b494f77b23afbe49c2f0fd2d"

func TestSystemPromptIdentityAndGoldenDigest(t *testing.T) {
	message := SystemPromptMessage()
	if message.Role != domain.PromptRoleSystem || message.Text == "" {
		t.Fatalf("message = %#v", message)
	}
	if PromptID != "och_coding_agent_v1" || PromptVersion != "1.0.0" {
		t.Fatalf("identity = %q/%q", PromptID, PromptVersion)
	}
	if PromptDigest != goldenPromptDigest {
		t.Fatalf("PromptDigest = %q, want %q", PromptDigest, goldenPromptDigest)
	}
	sum := sha256.Sum256([]byte(message.Text))
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != goldenPromptDigest {
		t.Fatalf("embedded prompt digest = %q, want %q", got, goldenPromptDigest)
	}
	if !utf8.ValidString(message.Text) {
		t.Fatal("embedded prompt is not valid UTF-8")
	}
	if !strings.HasSuffix(message.Text, "\n") {
		t.Fatal("embedded prompt must retain its final newline")
	}
}

func TestSystemPromptCarriesStableSafetyAndWorkflowGuidance(t *testing.T) {
	text := SystemPromptMessage().Text
	for _, required := range []string{
		"one admitted workspace",
		"More specific AGENTS.md instructions",
		"read it again and reconsider the edit",
		"not authorization",
		"approval, sandbox, workspace, credential, or tool-risk controls",
		"progress updates concise and factual",
		"fresh verification evidence",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("prompt missing required guidance %q", required)
		}
	}

	lower := strings.ToLower(text)
	for _, forbidden := range []string{
		"deepseek", "gpt-", "claude-", "api_key", "api-key",
		"session id", "sessionid", "{{", "${", "2026-",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("prompt contains dynamic or provider-specific text %q", forbidden)
		}
	}
}
