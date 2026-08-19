package sevenzip_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	sevenzip "github.com/inpadi/7zip"
)

func TestCreateOpenAndEditArchiveFilesystem(t *testing.T) {
	archiveName := filepath.Join(t.TempDir(), "data.7z")
	archive, err := sevenzip.Create(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.MkdirAll("docs/nested", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteFile("docs/readme.txt", []byte("first"), 0o640); err != nil {
		t.Fatal(err)
	}
	file, err := archive.Create("docs/nested/value.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("value"); err != nil {
		t.Fatal(err)
	}
	// Close must safely finish handles the caller leaves open.
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	archive, err = sevenzip.Open(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Format() != sevenzip.Format7z {
		t.Fatalf("format = %q", archive.Format())
	}
	assertFile(t, archive, "docs/readme.txt", "first")
	assertFile(t, archive, "docs/nested/value.txt", "value")
	if err := archive.Rename("docs/readme.txt", "README.txt"); err != nil {
		t.Fatal(err)
	}
	if err := archive.RemoveAll("docs/nested"); err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteFile("added.txt", []byte("added"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	archive, err = sevenzip.Open(archiveName, &sevenzip.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	assertFile(t, archive, "README.txt", "first")
	assertFile(t, archive, "added.txt", "added")
	if _, err := fs.Stat(archive, "docs/nested/value.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removed file stat error = %v", err)
	}
	if err := archive.WriteFile("blocked.txt", nil, 0o600); !errors.Is(err, sevenzip.ErrReadOnly) {
		t.Fatalf("read-only write error = %v", err)
	}
}

func TestDiscardAndUnchangedClosePreserveArchive(t *testing.T) {
	archiveName := filepath.Join(t.TempDir(), "data.zip")
	archive, err := sevenzip.Create(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteFile("value.txt", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(archiveName)
	if err != nil {
		t.Fatal(err)
	}

	archive, err = sevenzip.Open(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteFile("value.txt", []byte("discarded"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := archive.Discard(); err != nil {
		t.Fatal(err)
	}
	assertArchiveBytes(t, archiveName, original)

	archive, err = sevenzip.Open(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, archive, "value.txt", "original")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	assertArchiveBytes(t, archiveName, original)
}

func TestCloseRejectsArchiveChangedOutsideFilesystem(t *testing.T) {
	archiveName := filepath.Join(t.TempDir(), "data.tar")
	archive, err := sevenzip.Create(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteFile("value.txt", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	archive, err = sevenzip.Open(archiveName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteFile("value.txt", []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	external := []byte("external replacement")
	if err := os.WriteFile(archiveName, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); !errors.Is(err, sevenzip.ErrArchiveChanged) {
		t.Fatalf("close error = %v", err)
	}
	assertArchiveBytes(t, archiveName, external)
}

func TestCreateEmptyArchive(t *testing.T) {
	for _, extension := range []string{".7z", ".zip", ".tar.gz"} {
		t.Run(extension, func(t *testing.T) {
			archiveName := filepath.Join(t.TempDir(), "empty"+extension)
			archive, err := sevenzip.Create(archiveName, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			archive, err = sevenzip.Open(archiveName, nil)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := archive.ReadDir(".")
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("entries = %v", entries)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestArchivePathsFollowIOFSRules(t *testing.T) {
	archive, err := sevenzip.Create(filepath.Join(t.TempDir(), "paths.7z"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Discard()
	for _, name := range []string{"/absolute", "../escape", "a\\b"} {
		if err := archive.WriteFile(name, nil, 0o600); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteFile(%q) error = %v", name, err)
		}
	}
	if err := archive.RemoveAll("."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("RemoveAll root error = %v", err)
	}
}

func assertFile(t *testing.T, filesystem fs.FS, name, want string) {
	t.Helper()
	content, err := fs.ReadFile(filesystem, name)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("%s = %q, want %q", name, content, want)
	}
}

func assertArchiveBytes(t *testing.T, name string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("archive bytes changed: got %d bytes, want %d", len(got), len(want))
	}
}
