package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type fixtureDigestEntry struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Executable bool   `json:"executable,omitempty"`
	ByteLength int64  `json:"byteLength,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}

// DigestFixtureTree computes the frozen identity of a fixture source tree.
// The digest is SHA-256 over canonical JSON for its path-sorted directory and
// regular-file entries. File content, length, relative path, entry kind, and
// executable-bit state are bound; timestamps, ownership, and other permission
// bits are deliberately excluded because CopyFixture does not preserve them.
// The walk rejects every entry shape CopyFixture rejects.
func DigestFixtureTree(sourceDirectory string) (Digest, error) {
	if !filepath.IsAbs(sourceDirectory) {
		return "", fmt.Errorf("eval: digest fixture tree: source must be an absolute path")
	}

	entries := make([]fixtureDigestEntry, 0)
	err := filepath.WalkDir(sourceDirectory, func(walkPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", walkPath, walkErr)
		}
		relative, err := filepath.Rel(sourceDirectory, walkPath)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", walkPath, err)
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if err := validateContainedRelativePath(relative); err != nil {
			return fmt.Errorf("%s: %w", relative, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", relative, err)
		}
		if entry.IsDir() {
			entries = append(entries, fixtureDigestEntry{Path: relative, Kind: "directory"})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: %w: unsupported file type %s", relative, errFixtureRejected, info.Mode().Type())
		}
		links, err := hardLinkCount(walkPath, info)
		if err != nil {
			return fmt.Errorf("%s: check hard link count: %w", relative, err)
		}
		if links > 1 {
			return fmt.Errorf("%s: %w: hard-linked file", relative, errFixtureRejected)
		}

		file, err := os.Open(walkPath)
		if err != nil {
			return fmt.Errorf("open %s: %w", relative, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("hash %s: %w", relative, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", relative, closeErr)
		}
		entries = append(entries, fixtureDigestEntry{
			Path: relative, Kind: "file", Executable: info.Mode()&0o111 != 0,
			ByteLength: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)),
		})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("eval: digest fixture tree: %w", err)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("eval: digest fixture tree: encode: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return Digest("sha256:" + hex.EncodeToString(sum[:])), nil
}
