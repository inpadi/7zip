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

func TestParentCacheRevalidatesMovedDirectory(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	root, err := OpenExtractionRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cache := NewParentCache(root, true)
	defer cache.Close()

	first, err := cache.Directory("parent", 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := cache.Directory("parent", 0o755); err != nil || again != first {
		t.Fatalf("cache reuse = %p, %v; want %p", again, err, first)
	}

	moved := filepath.Join(output, "moved")
	if err := os.Rename(filepath.Join(output, "parent"), moved); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission) {
			t.Skip("Windows filesystem does not allow renaming an open directory")
		}
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(output, "parent"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := cache.Directory("parent", 0o755)
	if err != nil {
		t.Fatal(err)
	}
	file, err := second.OpenFile("new.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "parent", "new.txt")); err != nil {
		t.Fatalf("replacement directory did not receive the file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("moved cached directory received the file: %v", err)
	}
}

func TestParentCacheWithoutRevalidationRemainsPinned(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	root, err := OpenExtractionRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cache := NewParentCache(root, false)
	defer cache.Close()

	first, err := cache.Directory("parent", 0o755)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(output, "moved")
	if err := os.Rename(filepath.Join(output, "parent"), moved); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission) {
			t.Skip("Windows filesystem does not allow renaming an open directory")
		}
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(output, "parent"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := cache.Directory("parent", 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("non-revalidating cache did not retain the pinned directory")
	}
	file, err := second.OpenFile("new.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "new.txt")); err != nil {
		t.Fatalf("pinned directory did not receive the file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "parent", "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("replacement directory received the file: %v", err)
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
