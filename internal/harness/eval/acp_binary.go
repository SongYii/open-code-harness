package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// ACPBinaryIdentity is one resolved och binary's frozen identity (design
// §11's ACPSubprocessIdentity.BinarySHA256): the absolute path this run
// actually launches and the SHA-256 of its exact bytes. A caller resolves
// this once per run and reuses it for every Attempt (design §12: "Build/
// resolve the exact och binary once per run") rather than re-hashing on
// every launch.
type ACPBinaryIdentity struct {
	Path   string
	SHA256 string
}

// ResolveACPBinary hashes the och binary already built or installed at
// path. Building or installing it is the caller's own responsibility —
// this package never invokes a Go toolchain itself, matching how this
// repository's own ACP interoperability tests (cmd/acp-client's own
// buildOchBinary) already build it externally before driving it as a
// real subprocess.
func ResolveACPBinary(path string) (ACPBinaryIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return ACPBinaryIdentity{}, fmt.Errorf("eval: resolve acp binary: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ACPBinaryIdentity{}, fmt.Errorf("eval: resolve acp binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ACPBinaryIdentity{}, fmt.Errorf("eval: resolve acp binary: %q is not a regular file", path)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return ACPBinaryIdentity{}, fmt.Errorf("eval: resolve acp binary: %w", err)
	}
	return ACPBinaryIdentity{Path: path, SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}
