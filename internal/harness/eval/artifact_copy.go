package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// errArtifactRejected marks a workspace entry evidence collection refuses
// to stage at all, rather than silently skip (design §8/§14's fixture-
// isolation discipline applied symmetrically on the way out): a symlink, a
// hard-linked file, a socket, a device, a FIFO, or any other non-regular
// type.
var errArtifactRejected = errors.New("eval: artifact rejected")

// errArtifactTruncated marks a workspace entry that is itself a regular
// file but exceeds the bound this collection enforces.
var errArtifactTruncated = errors.New("eval: artifact truncated")

// stagedArtifact is what stageWorkspaceArtifact resolved a source path to,
// in a shape ready to become a ManifestEntry.
type stagedArtifact struct {
	sha256     string
	byteLength int64
}

// stageWorkspaceArtifact copies exactly one already-Lstat-validated
// regular file from the live Attempt workspace into destinationPath (which
// must not already exist), streaming a SHA-256 digest and refusing to copy
// more than maxBytes. It never follows a symlink and never copies a
// hard-linked file, matching CopyFixture's own discipline on the way a
// Scenario's fixture enters the workspace, applied symmetrically to what
// leaves it as evidence. Its own error wraps errArtifactRejected (a type
// this collector will never retry or truncate around) or
// errArtifactTruncated (a size this collector records rather than
// silently accepting a partial copy of).
func stageWorkspaceArtifact(sourcePath, destinationPath string, maxBytes int64) (stagedArtifact, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return stagedArtifact{}, err
	}
	if !info.Mode().IsRegular() {
		return stagedArtifact{}, fmt.Errorf("%w: unsupported file type %s", errArtifactRejected, info.Mode().Type())
	}
	links, err := hardLinkCount(sourcePath, info)
	if err != nil {
		return stagedArtifact{}, fmt.Errorf("check hard link count: %w", err)
	}
	if links > 1 {
		return stagedArtifact{}, fmt.Errorf("%w: hard-linked file", errArtifactRejected)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return stagedArtifact{}, err
	}
	defer source.Close()

	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return stagedArtifact{}, err
	}

	hasher := sha256.New()
	// Read one byte beyond maxBytes so an over-limit source is detected
	// from the actual bytes read, not trusted from the Lstat size alone.
	written, copyErr := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(source, maxBytes+1))
	if copyErr != nil {
		_ = destination.Close()
		_ = os.Remove(destinationPath)
		return stagedArtifact{}, copyErr
	}
	if written > maxBytes {
		_ = destination.Close()
		_ = os.Remove(destinationPath)
		return stagedArtifact{}, fmt.Errorf("%w: exceeds max bytes (%d)", errArtifactTruncated, maxBytes)
	}
	if err := destination.Close(); err != nil {
		return stagedArtifact{}, err
	}
	return stagedArtifact{sha256: hex.EncodeToString(hasher.Sum(nil)), byteLength: written}, nil
}

// digestFile streams sourcePath's exact already-written bytes and returns
// its SHA-256, without copying it anywhere: used for a document this
// collector itself already published in place (the Outcome copy), where
// digesting is all that is needed.
func digestFile(path string) (stagedArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return stagedArtifact{}, err
	}
	defer file.Close()
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return stagedArtifact{}, err
	}
	return stagedArtifact{sha256: hex.EncodeToString(hasher.Sum(nil)), byteLength: written}, nil
}
