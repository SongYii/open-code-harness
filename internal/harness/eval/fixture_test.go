package eval

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewAttemptRootCreatesEverySubdirectory(t *testing.T) {
	base := t.TempDir()
	attemptID := mustAttemptID(t)
	directories, err := NewAttemptRoot(base, attemptID)
	if err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}
	for name, path := range map[string]string{
		"workspace": directories.Workspace,
		"database":  directories.Database,
		"audit":     directories.Audit,
		"process":   directories.Process,
		"log":       directories.Log,
		"evidence":  directories.Evidence,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s): %v", name, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", name)
		}
	}
}

func TestNewAttemptRootNeverReusesAnExistingRoot(t *testing.T) {
	base := t.TempDir()
	attemptID := mustAttemptID(t)
	if _, err := NewAttemptRoot(base, attemptID); err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}
	if _, err := NewAttemptRoot(base, attemptID); err == nil {
		t.Fatal("NewAttemptRoot succeeded a second time for the same Attempt ID")
	}
}

func generousLimits() FixtureCopyLimits {
	return FixtureCopyLimits{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}
}

func TestCopyFixtureHappyPath(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), "hello", 0o644)
	if err := os.Mkdir(filepath.Join(source, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeFile(t, filepath.Join(source, "sub", "run.sh"), "#!/bin/sh\necho hi\n", 0o755)

	result, err := CopyFixture(source, destination, generousLimits())
	if err != nil {
		t.Fatalf("CopyFixture: %v", err)
	}
	if result.FileCount != 2 {
		t.Fatalf("FileCount = %d, want 2", result.FileCount)
	}
	if result.TotalBytes != int64(len("hello")+len("#!/bin/sh\necho hi\n")) {
		t.Fatalf("TotalBytes = %d, want %d", result.TotalBytes, len("hello")+len("#!/bin/sh\necho hi\n"))
	}

	plainInfo, err := os.Stat(filepath.Join(destination, "a.txt"))
	if err != nil {
		t.Fatalf("Stat(a.txt): %v", err)
	}
	if plainInfo.Mode().Perm() != 0o600 {
		t.Fatalf("a.txt mode = %v, want 0600 (non-executable source must not become executable)", plainInfo.Mode().Perm())
	}
	scriptInfo, err := os.Stat(filepath.Join(destination, "sub", "run.sh"))
	if err != nil {
		t.Fatalf("Stat(sub/run.sh): %v", err)
	}
	if scriptInfo.Mode().Perm() != 0o700 {
		t.Fatalf("run.sh mode = %v, want 0700 (executable source must stay executable)", scriptInfo.Mode().Perm())
	}
}

func TestCopyFixtureRejectsSymlink(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "real.txt"), "hello", 0o644)
	if err := os.Symlink(filepath.Join(source, "real.txt"), filepath.Join(source, "link.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := CopyFixture(source, destination, generousLimits()); !errors.Is(err, errFixtureRejected) {
		t.Fatalf("CopyFixture error = %v, want wrapping errFixtureRejected", err)
	}
}

func TestCopyFixtureRejectsHardLinkedFile(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "original.txt"), "hello", 0o644)
	if err := os.Link(filepath.Join(source, "original.txt"), filepath.Join(source, "hardlink.txt")); err != nil {
		t.Skipf("hard links unsupported in this test environment: %v", err)
	}
	if _, err := CopyFixture(source, destination, generousLimits()); !errors.Is(err, errFixtureRejected) {
		t.Fatalf("CopyFixture error = %v, want wrapping errFixtureRejected", err)
	}
}

func TestCopyFixtureEnforcesMaxFiles(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), "a", 0o644)
	writeFile(t, filepath.Join(source, "b.txt"), "b", 0o644)
	limits := FixtureCopyLimits{MaxFiles: 1, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}
	if _, err := CopyFixture(source, destination, limits); err == nil {
		t.Fatal("CopyFixture accepted more files than limits.maxFiles")
	}
}

func TestCopyFixtureEnforcesMaxFileBytes(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), "0123456789", 0o644)
	limits := FixtureCopyLimits{MaxFiles: 10, MaxFileBytes: 5, MaxTotalBytes: 1 << 20}
	if _, err := CopyFixture(source, destination, limits); err == nil {
		t.Fatal("CopyFixture accepted a file larger than limits.maxFileBytes")
	}
}

func TestCopyFixtureEnforcesMaxTotalBytes(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), "12345", 0o644)
	writeFile(t, filepath.Join(source, "b.txt"), "12345", 0o644)
	limits := FixtureCopyLimits{MaxFiles: 10, MaxFileBytes: 10, MaxTotalBytes: 5}
	if _, err := CopyFixture(source, destination, limits); err == nil {
		t.Fatal("CopyFixture accepted an aggregate size larger than limits.maxTotalBytes")
	}
}

func TestCopyFixtureRejectsNonEmptyDestination(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), "hello", 0o644)
	writeFile(t, filepath.Join(destination, "preexisting.txt"), "already here", 0o644)
	if _, err := CopyFixture(source, destination, generousLimits()); err == nil {
		t.Fatal("CopyFixture accepted a non-empty destination")
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
