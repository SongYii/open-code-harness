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

func TestResolveFixtureCopyLimitsUsesEvalSetDefaultForZeroMaxFiles(t *testing.T) {
	setLimits := validEvalSetLimits()
	policy := FixtureCopyPolicy{MaxFileBytes: 1024, MaxTotalBytes: 4096}
	resolved, err := ResolveFixtureCopyLimits(setLimits, policy)
	if err != nil {
		t.Fatalf("ResolveFixtureCopyLimits: %v", err)
	}
	if resolved.MaxFiles != DefaultFixtureFiles {
		t.Fatalf("MaxFiles = %d, want the EvalSet default %d", resolved.MaxFiles, DefaultFixtureFiles)
	}
	if resolved.MaxFileBytes != 1024 || resolved.MaxTotalBytes != 4096 {
		t.Fatalf("resolved = %+v, want the Scenario's own byte limits carried through", resolved)
	}
}

func TestResolveFixtureCopyLimitsRejectsWideningMaxFiles(t *testing.T) {
	setLimits := validEvalSetLimits()
	policy := FixtureCopyPolicy{MaxFiles: DefaultFixtureFiles + 1, MaxFileBytes: 1024, MaxTotalBytes: 4096}
	if _, err := ResolveFixtureCopyLimits(setLimits, policy); err == nil {
		t.Fatal("ResolveFixtureCopyLimits accepted a Scenario maxFiles wider than the EvalSet limit")
	}
}

func TestResolveFixtureCopyLimitsRequiresExplicitByteBounds(t *testing.T) {
	setLimits := validEvalSetLimits()
	if _, err := ResolveFixtureCopyLimits(setLimits, FixtureCopyPolicy{MaxTotalBytes: 4096}); err == nil {
		t.Fatal("ResolveFixtureCopyLimits accepted a zero maxFileBytes")
	}
	if _, err := ResolveFixtureCopyLimits(setLimits, FixtureCopyPolicy{MaxFileBytes: 1024}); err == nil {
		t.Fatal("ResolveFixtureCopyLimits accepted a zero maxTotalBytes")
	}
}

func TestRefuseArtifactRootWithinFixtureRejectsNesting(t *testing.T) {
	tests := []struct {
		name         string
		artifactRoot string
		fixtureRoot  string
	}{
		{"artifact inside fixture", "/repo/eval/scenarios/s1/fixture/.eval", "/repo/eval/scenarios/s1/fixture"},
		{"fixture inside artifact", "/repo/.eval", "/repo/.eval/scenarios/s1/fixture"},
		{"identical", "/repo/.eval", "/repo/.eval"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := RefuseArtifactRootWithinFixture(test.artifactRoot, test.fixtureRoot); err == nil {
				t.Fatalf("RefuseArtifactRootWithinFixture(%q, %q) accepted nested roots", test.artifactRoot, test.fixtureRoot)
			}
		})
	}
}

func TestRefuseArtifactRootWithinFixtureAllowsDisjointRoots(t *testing.T) {
	if err := RefuseArtifactRootWithinFixture("/repo/.eval", "/repo/eval/scenarios/s1/fixture"); err != nil {
		t.Fatalf("RefuseArtifactRootWithinFixture rejected disjoint roots: %v", err)
	}
}

func TestRefuseArtifactRootWithinFixtureRequiresAbsolutePaths(t *testing.T) {
	if err := RefuseArtifactRootWithinFixture("relative", "/repo/fixture"); err == nil {
		t.Fatal("RefuseArtifactRootWithinFixture accepted a relative artifactRoot")
	}
}

func TestDigestFixtureTreeBindsContentPathsAndExecutableBit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "one", 0o644)
	first, err := DigestFixtureTree(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DigestFixtureTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same tree digests differ: %q vs %q", first, second)
	}

	writeFile(t, filepath.Join(root, "a.txt"), "two", 0o644)
	contentChanged, err := DigestFixtureTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if contentChanged == first {
		t.Fatal("content change did not change fixture digest")
	}

	if err := os.Chmod(filepath.Join(root, "a.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	executableChanged, err := DigestFixtureTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if executableChanged == contentChanged {
		t.Fatal("executable-bit change did not change fixture digest")
	}
}

func TestDigestFixtureTreeRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := DigestFixtureTree(root); !errors.Is(err, errFixtureRejected) {
		t.Fatalf("DigestFixtureTree() error = %v, want fixture rejection", err)
	}
}
