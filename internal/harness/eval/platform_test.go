package eval

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHardLinkCountDirectly pins hardLinkCount's own contract (implementation
// plan Task 3), independent of CopyFixture's higher-level rejection: an
// ordinary regular file has a link count of 1 on whichever platform this
// test runs on, and a hard-linked file has more than 1.
func TestHardLinkCountDirectly(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "a.txt")
	writeFile(t, path, "hello", 0o644)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	count, err := hardLinkCount(path, info)
	if err != nil {
		t.Fatalf("hardLinkCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("hardLinkCount(ordinary file) = %d, want 1", count)
	}

	linkPath := filepath.Join(directory, "b.txt")
	if err := os.Link(path, linkPath); err != nil {
		t.Skipf("hard links unsupported in this test environment: %v", err)
	}
	linkedInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	linkedCount, err := hardLinkCount(path, linkedInfo)
	if err != nil {
		t.Fatalf("hardLinkCount: %v", err)
	}
	if linkedCount <= 1 {
		t.Fatalf("hardLinkCount(hard-linked file) = %d, want > 1", linkedCount)
	}
}
