package cli

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf16"
)

func TestParseExtract(t *testing.T) {
	opts, err := Parse([]string{"x", "-y", "-pSecret", "-ooutput", "-t7z", "archive.7z", "dir/*"})
	if err != nil {
		t.Fatal(err)
	}
	want := Options{
		Command:          CommandExtract,
		Archive:          "archive.7z",
		Files:            []string{"dir/*"},
		OutputDir:        "output",
		Password:         "Secret",
		Overwrite:        OverwriteAll,
		Solid:            true,
		Format:           "7z",
		ListCharset:      "utf-8",
		CompressionLevel: -1,
	}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("Parse() = %#v, want %#v", opts, want)
	}
}

func TestParseStopsSwitchProcessing(t *testing.T) {
	opts, err := Parse([]string{"l", "--", "-archive.7z", "-literal-name"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Archive != "-archive.7z" || !reflect.DeepEqual(opts.Files, []string{"-literal-name"}) {
		t.Fatalf("unexpected options: %#v", opts)
	}
}

func TestParseEncryptedSolidAdd(t *testing.T) {
	opts, err := Parse([]string{"a", "-pSecret", "-mhe=on", "-ms=off", "archive.7z", "input"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Password != "Secret" || !opts.HeaderEncryption || opts.Solid ||
		!opts.HeaderEncryptionDefined || !opts.SolidDefined {
		t.Fatalf("unexpected options: %#v", opts)
	}
}

func TestParseStreamsAndCompression(t *testing.T) {
	opts, err := Parse([]string{"a", "-sidata.txt", "-so", "-mx=7", "-m0=lzma", "archive.7z"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Stdin || opts.StdinName != "data.txt" || !opts.Stdout || opts.CompressionLevel != 7 ||
		!opts.CompressionLevelDefined || opts.Method != "lzma" {
		t.Fatalf("unexpected options: %#v", opts)
	}

	opts, err = Parse([]string{"x", "-si", "-so", "-ttar"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Archive != "-" || !opts.Stdin || !opts.Stdout {
		t.Fatalf("unexpected streamed extract options: %#v", opts)
	}
}

func TestParseExtractionPublication(t *testing.T) {
	opts, err := Parse([]string{"x", "-mep=atomic", "archive.7z"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Publication != PublicationAtomic || !opts.PublicationDefined {
		t.Fatalf("unexpected publication options: %#v", opts)
	}

	if _, err := Parse([]string{"a", "-mep=direct", "archive.7z", "input"}); err == nil {
		t.Fatal("creation unexpectedly accepted an extraction publication mode")
	}
	if _, err := Parse([]string{"x", "-mep=unknown", "archive.7z"}); err == nil {
		t.Fatal("extract unexpectedly accepted an unknown publication mode")
	}
}

func TestParseExecutableFilters(t *testing.T) {
	opts, err := Parse([]string{"a", "-mf=off", "archive.7z", "input"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.DisableFilters || !opts.FiltersDefined {
		t.Fatalf("unexpected executable filter options: %#v", opts)
	}

	if _, err := Parse([]string{"x", "-mf=off", "archive.7z"}); err == nil {
		t.Fatal("extraction unexpectedly accepted an executable filter mode")
	}
	if _, err := Parse([]string{"a", "-mf=unknown", "archive.7z", "input"}); err == nil {
		t.Fatal("creation unexpectedly accepted an unknown executable filter mode")
	}
}

func TestParseListFiles(t *testing.T) {
	root := t.TempDir()
	utf8List := filepath.Join(root, "utf8.lst")
	if err := os.WriteFile(utf8List, []byte("one file.txt\ntwo.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := Parse([]string{"a", "archive.7z", "@" + utf8List})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Files, []string{"one file.txt", "two.txt"}) {
		t.Fatalf("UTF-8 list entries = %q", opts.Files)
	}

	utf16List := filepath.Join(root, "utf16.lst")
	units := append([]uint16{0xfeff}, utf16.Encode([]rune("three.txt\nfour.txt"))...)
	content := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(content[i*2:], unit)
	}
	if err := os.WriteFile(utf16List, content, 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err = Parse([]string{"a", "-scsUTF-16LE", "archive.7z", "@" + utf16List})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Files, []string{"three.txt", "four.txt"}) {
		t.Fatalf("UTF-16 list entries = %q", opts.Files)
	}
}

func TestParseWildcardSelection(t *testing.T) {
	root := t.TempDir()
	excludes := filepath.Join(root, "exclude.lst")
	if err := os.WriteFile(excludes, []byte("*.tmp\ncache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := Parse([]string{"l", "-ba", "-r", "-i!*.txt", "-x@" + excludes, "archive.7z"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Bare || !opts.Recursive {
		t.Fatalf("output/recursion options = %#v", opts)
	}
	if !reflect.DeepEqual(opts.IncludePatterns, []string{"*.txt"}) {
		t.Fatalf("include patterns = %q", opts.IncludePatterns)
	}
	if !reflect.DeepEqual(opts.ExcludePatterns, []string{"*.tmp", "cache"}) {
		t.Fatalf("exclude patterns = %q", opts.ExcludePatterns)
	}
}

func TestParseRejectsUnsupportedBehavior(t *testing.T) {
	for _, args := range [][]string{
		{"a", "-tbogus", "archive.zip", "file"},
		{"x", "-p", "archive.7z"},
		{"x", "-slt", "archive.7z"},
		{"a", "-mhe=on", "archive.7z", "file"},
		{"a", "-ms=maybe", "archive.7z", "file"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", args)
		}
	}
}

func TestParseHelp(t *testing.T) {
	_, err := Parse(nil)
	if !errors.Is(err, ErrHelp) {
		t.Fatalf("Parse(nil) error = %v, want ErrHelp", err)
	}
}
