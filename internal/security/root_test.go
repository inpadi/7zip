package security

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtractionRootPublishesThroughPinnedDirectory(t *testing.T) {
	output := filepath.Join(t.TempDir(), "nested", "output")
	root, err := OpenExtractionRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	parent, err := root.MkdirRoot("one/two", 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	tempName, file, err := parent.CreateTemp()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("content"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Link(tempName, "result.txt"); err != nil {
		t.Fatal(err)
	}
	if err := parent.Remove(tempName); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(output, "one", "two", "result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "content" {
		t.Fatalf("content = %q", content)
	}
}

func TestExtractionRootRejectsSymlinkComponent(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		if runtime.GOOS == "windows" || errors.Is(err, fs.ErrPermission) {
			t.Skip("creating symlinks is not permitted")
		}
		t.Fatal(err)
	}
	root, err := OpenExtractionRoot(filepath.Join(link, "output"))
	if root != nil {
		root.Close()
	}
	if err == nil {
		t.Fatal("expected symlinked extraction path to be rejected")
	}
}

func TestSafeFileModeClearsGroupAndWorldWrite(t *testing.T) {
	if got := SafeFileMode(0o777); got != 0o755 {
		t.Fatalf("SafeFileMode(0777) = %#o", got)
	}
	if got := SafeFileMode(0o666); got != 0o644 {
		t.Fatalf("SafeFileMode(0666) = %#o", got)
	}
	if got := SafeFileMode(0o444); got != 0o644 {
		t.Fatalf("SafeFileMode(0444) = %#o", got)
	}
}
