package archivefmt

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestSourceWildcardRecursion(t *testing.T) {
	root := t.TempDir()
	for name := range map[string]string{
		"root.txt":          "root",
		"root.bin":          "bin",
		"nested/deep.txt":   "deep",
		"nested/deeper.bin": "deeper",
	} {
		name := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pattern := filepath.Join(root, "*.txt")
	for _, test := range []struct {
		name      string
		recursive bool
		want      []string
	}{
		{name: "current", want: []string{"root.txt"}},
		{name: "recursive", recursive: true, want: []string{"deep.txt", "root.txt"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			matches, err := sourceMatches(pattern, test.recursive)
			if err != nil {
				t.Fatal(err)
			}
			for i := range matches {
				matches[i] = filepath.Base(matches[i])
			}
			sort.Strings(matches)
			if !reflect.DeepEqual(matches, test.want) {
				t.Fatalf("matches = %q, want %q", matches, test.want)
			}
		})
	}
}

func TestAddRecursiveExcludes(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	for name, content := range map[string]string{
		"keep.txt":        "keep",
		"skip.tmp":        "skip",
		"nested/deep.tmp": "deep",
	} {
		name = filepath.Join(input, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, extension := range []string{".7z", ".zip", ".tar"} {
		t.Run(extension, func(t *testing.T) {
			archive := filepath.Join(root, "filtered"+extension)
			if _, err := Add(archive, []string{input}, AddOptions{Recursive: true, Excludes: []string{"*.tmp"}}); err != nil {
				t.Fatal(err)
			}
			entries, err := List(archive, "", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name)
			}
			if !reflect.DeepEqual(names, []string{"input", "input/keep.txt", "input/nested"}) &&
				!reflect.DeepEqual(names, []string{"input/", "input/keep.txt", "input/nested/"}) {
				t.Fatalf("entries = %q", names)
			}
		})
	}
}
