package archivefmt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inpadi/7zip/internal/security"
)

func TestExtractEntryClampsArchivedPermissions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	root, err := extractionRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var budget security.Budget
	content := "data"
	_, wrote, err := extractEntry(root, "world-writable.txt", 0o666, uint64(len(content)), strings.NewReader(content), ExtractOptions{}, make(map[string]string), &budget)
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

func TestISODirectoryExtentMetadataLimit(t *testing.T) {
	archive := new(isoArchive)
	size := int64(security.MaxMetadataBytes + 1)
	err := archive.walk(isoEntry{size: size}, "", false, size, make(map[[2]int64]bool), 0)
	if err == nil {
		t.Fatal("expected oversized ISO directory extent to be rejected")
	}
}
