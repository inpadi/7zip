package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputPublishesNewFileWithoutReplacement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "archive.7z")
	output, err := CreateOutput(target)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()
	if _, err := output.File().WriteString("generated"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("raced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := output.Publish(); err == nil {
		t.Fatal("expected publication to reject a destination created during the operation")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "raced" {
		t.Fatalf("destination was replaced: %q", data)
	}
}

func TestOutputReplacesPinnedRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "archive.7z")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := CreateOutput(target)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()
	if !output.Existed() {
		t.Fatal("expected the output transaction to record the existing archive")
	}
	if _, err := output.File().WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := output.Publish(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("unexpected published content: %q", data)
	}
}
