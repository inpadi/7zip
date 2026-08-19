package app

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelpAndUserError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("help exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage: 7zip") {
		t.Fatalf("help output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "This build is from inpadi ApS - support@inpadi.com for support / questions") {
		t.Fatalf("help output lacks publisher support header: %q", stdout.String())
	}

	stdout.Reset()
	if code := Run([]string{"a", "-mx10", "archive.7z", "file"}, &stdout, &stderr); code != ExitUserError {
		t.Fatalf("invalid switch exit code = %d", code)
	}
}

func TestRunAddListTestExtract(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "hello.7z")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"a", archive, source}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("add code = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"l", archive}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("list code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello.txt") {
		t.Fatalf("list output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"t", archive}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("test code = %d, stderr = %s", code, stderr.String())
	}

	output := filepath.Join(root, "output")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"x", "-o" + output, archive}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("extract code = %d, stderr = %s", code, stderr.String())
	}
	content, err := os.ReadFile(filepath.Join(output, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("extracted content = %q", content)
	}
}

func TestRunEncrypted7zAndZip(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	encrypted := filepath.Join(root, "hello-encrypted.7z")
	if code := Run([]string{"a", "-psecret", "-mhe=on", "-ms=off", encrypted, source}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("encrypted add code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"t", "-psecret", encrypted}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("encrypted test code = %d, stderr = %s", code, stderr.String())
	}

	archive := filepath.Join(root, "hello.zip")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"a", "-tzip", archive, source}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("ZIP add code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"t", archive}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("ZIP test code = %d, stderr = %s", code, stderr.String())
	}
	if err := os.WriteFile(source, []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"u", archive, source}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("ZIP update code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunStandardStreams(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(source, []byte("stream payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	var archive, stderr bytes.Buffer
	if code := RunWithIO([]string{"a", "-ttar", "-so", "ignored.tar", source}, strings.NewReader(""), &archive, &stderr); code != ExitSuccess {
		t.Fatalf("stdout archive code = %d, stderr = %s", code, stderr.String())
	}
	reader := tar.NewReader(bytes.NewReader(archive.Bytes()))
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "payload.txt" {
		t.Fatalf("streamed TAR name = %q", header.Name)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "stream payload" {
		t.Fatalf("streamed TAR payload = %q", payload)
	}

	var extracted bytes.Buffer
	stderr.Reset()
	if code := RunWithIO([]string{"x", "-si", "-so", "-ttar"}, bytes.NewReader(archive.Bytes()), &extracted, &stderr); code != ExitSuccess {
		t.Fatalf("stdin archive code = %d, stderr = %s", code, stderr.String())
	}
	if extracted.String() != "stream payload" {
		t.Fatalf("streamed extraction = %q", extracted.String())
	}

	sevenZip := filepath.Join(root, "stdin.7z")
	var stdout bytes.Buffer
	stderr.Reset()
	if code := RunWithIO([]string{"a", "-sidata.txt", sevenZip}, strings.NewReader("stdin payload"), &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("stdin payload code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"l", sevenZip}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("list stdin archive code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "data.txt") {
		t.Fatalf("stdin entry missing from list: %s", stdout.String())
	}
}

func TestRunBareListOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "hello.7z")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"a", archive, source}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("add code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"l", "-ba", archive}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("bare list code = %d, stderr = %s", code, stderr.String())
	}
	output := strings.TrimRight(stdout.String(), "\r\n")
	if strings.Contains(output, "7-Zip") || strings.Contains(output, "inpadi ApS") ||
		strings.Contains(output, "support@inpadi.com") || strings.Contains(output, "Listing archive") ||
		strings.Contains(output, "Date") {
		t.Fatalf("bare list contains headers: %q", output)
	}
	if lines := strings.Split(output, "\n"); len(lines) != 1 {
		t.Fatalf("bare list lines = %q", lines)
	}
	if !strings.HasSuffix(output, "  hello.txt") || !strings.Contains(output, "....A            5") {
		t.Fatalf("bare list row = %q", output)
	}
}

func TestRunBareListMatchesUpstream(t *testing.T) {
	upstream, err := exec.LookPath("7z")
	if err != nil {
		t.Skip("upstream 7z is not installed")
	}
	root := t.TempDir()
	files := map[string]string{
		"root.txt":              "root",
		"input/direct.txt":      "direct",
		"input/direct.bin":      "binary",
		"input/nested/deep.txt": "deep",
		"literal[1].txt":        "literal",
	}
	for name, content := range files {
		name = filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(root, "wildcards.7z")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"a", archive, filepath.Join(root, "root.txt"), filepath.Join(root, "literal[1].txt"), filepath.Join(root, "input")}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("add code = %d, stderr = %s", code, stderr.String())
	}
	for _, switches := range [][]string{
		{"*.txt"},
		{"-r", "*.txt"},
		{"input"},
		{"*"},
		{"literal[1].txt"},
	} {
		stdout.Reset()
		stderr.Reset()
		// Archive name precedes file masks in the 7-Zip command line.
		ours := append([]string{"l", "-ba", archive}, switches...)
		if code := Run(ours, &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("Run(%q) code = %d, stderr = %s", ours, code, stderr.String())
		}
		upstreamArgs := append([]string{"l", "-ba", archive}, switches...)
		want, err := exec.Command(upstream, upstreamArgs...).Output()
		if err != nil {
			t.Fatalf("upstream 7z %q: %v", upstreamArgs, err)
		}
		if !bytes.Equal(stdout.Bytes(), want) {
			t.Fatalf("bare listing mismatch for %q\nGo:\n%s\nUpstream:\n%s", switches, stdout.Bytes(), want)
		}
	}
}
