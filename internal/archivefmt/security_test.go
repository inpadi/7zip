package archivefmt

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inpadi/7zip/internal/security"
)

type extractionErrorReader struct {
	content string
	done    bool
}

func (r *extractionErrorReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.content), nil
	}
	return 0, errors.New("injected read failure")
}

func TestExtractEntryClampsArchivedPermissions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	root, err := extractionRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	parents := extractionParents(root, ExtractOptions{})
	defer parents.Close()
	var budget security.Budget
	content := "data"
	_, wrote, err := extractEntry(parents, "world-writable.txt", 0o666, uint64(len(content)), strings.NewReader(content), ExtractOptions{}, make(map[string]string), &budget)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("entry was not written")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(output, "world-writable.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o022 != 0 {
			t.Fatalf("extracted mode remains writable by group or world: %#o", info.Mode().Perm())
		}
	}
}

func TestDirectExtractionRemovesPartialFile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	root, err := extractionRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	options := ExtractOptions{Publication: PublicationDirect}
	parents := extractionParents(root, options)
	defer parents.Close()
	var budget security.Budget
	_, wrote, err := extractEntry(parents, "partial.txt", 0o644, 0, &extractionErrorReader{content: "partial"}, options, make(map[string]string), &budget)
	if err == nil || wrote {
		t.Fatalf("extractEntry error = %v, wrote = %v", err, wrote)
	}
	if _, err := os.Stat(filepath.Join(output, "partial.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial direct output remains: %v", err)
	}
}

func TestExtractionPublicationVisibility(t *testing.T) {
	for _, test := range []struct {
		name        string
		publication PublicationMode
		visible     bool
	}{
		{name: "direct", publication: PublicationDirect, visible: true},
		{name: "atomic", publication: PublicationAtomic, visible: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			root, err := extractionRoot(output)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			options := ExtractOptions{Publication: test.publication}
			parents := extractionParents(root, options)
			defer parents.Close()
			target := filepath.Join(output, "value.txt")
			reader := readerFunc(func(p []byte) (int, error) {
				_, statErr := os.Stat(target)
				if (statErr == nil) != test.visible {
					t.Errorf("target visibility during decode = %v, want %v", statErr == nil, test.visible)
				}
				return copy(p, "value"), io.EOF
			})
			var budget security.Budget
			if _, wrote, err := extractEntry(parents, "value.txt", 0o644, 5, reader, options, make(map[string]string), &budget); err != nil || !wrote {
				t.Fatalf("extractEntry error = %v, wrote = %v", err, wrote)
			}
		})
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestISODirectoryExtentMetadataLimit(t *testing.T) {
	archive := new(isoArchive)
	size := int64(security.MaxMetadataBytes + 1)
	err := archive.walk(isoEntry{size: size}, "", false, size, make(map[[2]int64]bool), 0)
	if err == nil {
		t.Fatal("expected oversized ISO directory extent to be rejected")
	}
}
