package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveACPBinaryHashesExactBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	content := []byte("not a real binary, just some bytes to hash")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	identity, err := ResolveACPBinary(path)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	if identity.Path != path {
		t.Fatalf("Path = %q, want %q", identity.Path, path)
	}
	if identity.SHA256 != want {
		t.Fatalf("SHA256 = %q, want %q", identity.SHA256, want)
	}
}

func TestResolveACPBinaryDetectsContentChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("version one"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := ResolveACPBinary(path)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	if err := os.WriteFile(path, []byte("version two, different bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := ResolveACPBinary(path)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	if first.SHA256 == second.SHA256 {
		t.Fatal("ResolveACPBinary produced the same hash for two different binary contents")
	}
}

func TestResolveACPBinaryRejectsMissingFile(t *testing.T) {
	_, err := ResolveACPBinary(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("ResolveACPBinary() error = nil, want a refusal for a missing file")
	}
}

func TestResolveACPBinaryRejectsDirectory(t *testing.T) {
	_, err := ResolveACPBinary(t.TempDir())
	if err == nil {
		t.Fatal("ResolveACPBinary() error = nil, want a refusal for a directory")
	}
}
