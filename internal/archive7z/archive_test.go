package archive7z

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	mustWriteFile(t, filepath.Join(input, "hello.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(input, "nested", "world.txt"), []byte("world"))
	mustWriteFile(t, filepath.Join(input, "empty"), nil)

	archive := filepath.Join(root, "roundtrip.7z")
	result, err := Add(archive, []string{input})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 3 || result.Bytes != 10 {
		t.Fatalf("Add result = %#v", result)
	}

	entries, err := List(archive, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	wantNames := []string{"input/", "input/empty", "input/hello.txt", "input/nested/", "input/nested/world.txt"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("archive names = %q, want %q", names, wantNames)
	}

	tested, err := Test(archive, "", []string{"input"})
	if err != nil {
		t.Fatal(err)
	}
	if tested.Files != 3 || tested.Bytes != 10 {
		t.Fatalf("Test result = %#v", tested)
	}

	output := filepath.Join(root, "output")
	extracted, err := Extract(archive, ExtractOptions{OutputDir: output})
	if err != nil {
		t.Fatal(err)
	}
	if extracted.Files != 3 || extracted.Bytes != 10 {
		t.Fatalf("Extract result = %#v", extracted)
	}
	assertFileContent(t, filepath.Join(output, "input", "hello.txt"), []byte("hello"))
	assertFileContent(t, filepath.Join(output, "input", "nested", "world.txt"), []byte("world"))
	assertFileContent(t, filepath.Join(output, "input", "empty"), nil)
}

func TestExtractOverwritePolicies(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "value.txt")
	mustWriteFile(t, source, []byte("new"))
	archive := filepath.Join(root, "value.7z")
	if _, err := Add(archive, []string{source}); err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(root, "output")
	target := filepath.Join(output, "value.txt")
	mustWriteFile(t, target, []byte("old"))
	if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err == nil {
		t.Fatal("extract without overwrite unexpectedly succeeded")
	}
	assertFileContent(t, target, []byte("old"))

	result, err := Extract(archive, ExtractOptions{OutputDir: output, Overwrite: OverwriteSkip})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 0 {
		t.Fatalf("skip result = %#v", result)
	}
	assertFileContent(t, target, []byte("old"))

	result, err = Extract(archive, ExtractOptions{OutputDir: output, Overwrite: OverwriteAll})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 {
		t.Fatalf("overwrite result = %#v", result)
	}
	assertFileContent(t, target, []byte("new"))
}

func TestUpdateExistingArchive(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "value.txt")
	keep := filepath.Join(root, "keep.txt")
	added := filepath.Join(root, "added.txt")
	mustWriteFile(t, source, []byte("old"))
	mustWriteFile(t, keep, []byte("keep"))
	archive := filepath.Join(root, "value.7z")
	if _, err := Add(archive, []string{source, keep}); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, source, []byte("new"))
	mustWriteFile(t, added, []byte("added"))
	result, err := Add(archive, []string{source, added})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Bytes != 8 {
		t.Fatalf("update result = %#v", result)
	}
	if _, err := Test(archive, "", nil); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "updated")
	if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(output, "value.txt"), []byte("new"))
	assertFileContent(t, filepath.Join(output, "keep.txt"), []byte("keep"))
	assertFileContent(t, filepath.Join(output, "added.txt"), []byte("added"))
}

func TestEncryptedCreation(t *testing.T) {
	upstream := findUpstream7z(t)
	root := t.TempDir()
	for _, encryptHeaders := range []bool{false, true} {
		name := "data"
		if encryptHeaders {
			name = "headers"
		}
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(root, "secret-"+name+".txt")
			mustWriteFile(t, source, []byte("classified payload"))
			archive := filepath.Join(root, "encrypted-"+name+".7z")
			if _, err := AddWithOptions(archive, []string{source}, AddOptions{
				Solid:            true,
				Password:         "secret",
				HeaderEncryption: encryptHeaders,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := Test(archive, "wrong", nil); err == nil {
				t.Fatal("wrong password unexpectedly succeeded")
			}
			if _, err := Test(archive, "secret", nil); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(upstream, "t", "-bd", "-psecret", archive)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("upstream encrypted test failed: %v\n%s", err, output)
			}
			wrong := exec.Command(upstream, "t", "-bd", "-pwrong", archive)
			if output, err := wrong.CombinedOutput(); err == nil {
				t.Fatalf("upstream accepted wrong password:\n%s", output)
			}

			mustWriteFile(t, source, []byte("updated secret"))
			if _, err := AddWithOptions(archive, []string{source}, AddOptions{
				Solid:            true,
				Password:         "secret",
				HeaderEncryption: encryptHeaders,
			}); err != nil {
				t.Fatalf("encrypted update: %v", err)
			}
			if _, err := Test(archive, "secret", nil); err != nil {
				t.Fatalf("test encrypted update: %v", err)
			}
			command = exec.Command(upstream, "t", "-bd", "-psecret", archive)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("upstream encrypted update test failed: %v\n%s", err, output)
			}
		})
	}
}

func TestNonSolidCreation(t *testing.T) {
	upstream := findUpstream7z(t)
	root := t.TempDir()
	one := filepath.Join(root, "one.txt")
	two := filepath.Join(root, "two.txt")
	mustWriteFile(t, one, []byte("one"))
	mustWriteFile(t, two, []byte("two"))
	archive := filepath.Join(root, "non-solid.7z")
	if _, err := AddWithOptions(archive, []string{one, two}, AddOptions{Solid: false}); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(upstream, "l", "-slt", archive)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("upstream list failed: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("Solid = -")) || !bytes.Contains(output, []byte("Blocks = 2")) {
		t.Fatalf("unexpected upstream metadata:\n%s", output)
	}
}

func TestCompressionMethods(t *testing.T) {
	upstream := findUpstream7z(t)
	for _, method := range []string{"copy", "lzma", "lzma2"} {
		t.Run(method, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "payload.txt")
			mustWriteFile(t, source, bytes.Repeat([]byte("compressible payload\n"), 256))
			archive := filepath.Join(root, method+".7z")
			if _, err := AddWithOptions(archive, []string{source}, AddOptions{
				Solid:        true,
				Level:        7,
				LevelDefined: true,
				Method:       method,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := Test(archive, "", nil); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(upstream, "t", "-bd", archive)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("upstream test failed: %v\n%s", err, output)
			}
			list := exec.Command(upstream, "l", "-slt", archive)
			output, err := list.CombinedOutput()
			if err != nil {
				t.Fatalf("upstream list failed: %v\n%s", err, output)
			}
			want := map[string][]byte{
				"copy":  []byte("Method = Copy"),
				"lzma":  []byte("Method = LZMA:"),
				"lzma2": []byte("Method = LZMA2:"),
			}[method]
			if !bytes.Contains(output, want) {
				t.Fatalf("upstream method metadata does not contain %q:\n%s", want, output)
			}
		})
	}
}

func TestFastLZMA2Compression(t *testing.T) {
	upstream := findUpstream7z(t)
	payload := make([]byte, 4<<20)
	pattern := []byte("fast lzma2 compression payload\n")
	for offset := 0; offset < len(payload); offset += 4096 {
		block := payload[offset:min(offset+4096, len(payload))]
		if offset%(8*4096) == 0 {
			for i := range block {
				block[i] = byte((offset + i*31) >> 3)
			}
			continue
		}
		for i := range block {
			block[i] = pattern[i%len(pattern)]
		}
	}

	for _, password := range []string{"", "fast-password"} {
		name := "plain"
		if password != "" {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "payload.bin")
			mustWriteFile(t, source, payload)
			archive := filepath.Join(root, "fast.7z")
			if _, err := AddWithOptions(archive, []string{source}, AddOptions{
				Solid:        true,
				Password:     password,
				Level:        1,
				LevelDefined: true,
				Method:       "lzma2",
			}); err != nil {
				t.Fatal(err)
			}
			var decoded bytes.Buffer
			if _, err := WriteContents(archive, password, nil, &decoded); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded.Bytes(), payload) {
				t.Fatal("decoded fast LZMA2 payload differs from input")
			}
			args := []string{"t", "-bd"}
			if password != "" {
				args = append(args, "-p"+password)
			}
			args = append(args, archive)
			if output, err := exec.Command(upstream, args...).CombinedOutput(); err != nil {
				t.Fatalf("upstream test failed: %v\n%s", err, output)
			}
		})
	}
}

func TestUpstreamInteroperability(t *testing.T) {
	upstream := findUpstream7z(t)
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "input", "nested", "payload.txt"), []byte("interoperable"))

	t.Run("upstream reads Go archive", func(t *testing.T) {
		archive := filepath.Join(root, "go.7z")
		extra := filepath.Join(root, "extra.txt")
		mustWriteFile(t, extra, []byte("second solid stream"))
		if _, err := Add(archive, []string{filepath.Join(root, "input"), extra}); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(upstream, "t", "-bd", archive)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("upstream test failed: %v\n%s", err, output)
		}
		list := exec.Command(upstream, "l", "-slt", archive)
		listOutput, err := list.CombinedOutput()
		if err != nil {
			t.Fatalf("upstream list failed: %v\n%s", err, listOutput)
		}
		if !bytes.Contains(listOutput, []byte("Solid = +")) || !bytes.Contains(listOutput, []byte("Blocks = 1")) {
			t.Fatalf("Go archive is not solid:\n%s", listOutput)
		}
	})

	t.Run("Go reads upstream archive", func(t *testing.T) {
		archive := filepath.Join(root, "upstream.7z")
		command := exec.Command(upstream, "a", "-bd", "-y", "-t7z", "-m0=LZMA2", archive, "input")
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("upstream create failed: %v\n%s", err, output)
		}
		result, err := Test(archive, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Files != 1 || result.Bytes != uint64(len("interoperable")) {
			t.Fatalf("Test result = %#v", result)
		}
		output := filepath.Join(root, "extracted")
		if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
			t.Fatal(err)
		}
		assertFileContent(t, filepath.Join(output, "input", "nested", "payload.txt"), []byte("interoperable"))
	})

	t.Run("Go reads encrypted upstream archive", func(t *testing.T) {
		archive := filepath.Join(root, "upstream-encrypted.7z")
		command := exec.Command(upstream, "a", "-bd", "-y", "-t7z", "-m0=LZMA2", "-mhe=on", "-psecret", archive, "input")
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("upstream encrypted create failed: %v\n%s", err, output)
		}
		if _, err := Test(archive, "wrong", nil); err == nil {
			t.Fatal("test with wrong password unexpectedly succeeded")
		}
		result, err := Test(archive, "secret", nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Files != 1 || result.Bytes != uint64(len("interoperable")) {
			t.Fatalf("encrypted Test result = %#v", result)
		}
	})

	t.Run("Go update preserves missing timestamps", func(t *testing.T) {
		archive := filepath.Join(root, "upstream-no-time.7z")
		command := exec.Command(upstream, "a", "-bd", "-y", "-t7z", "-mtm=off", archive, "input")
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("upstream create without times failed: %v\n%s", err, output)
		}
		extra := filepath.Join(root, "no-time-extra.txt")
		mustWriteFile(t, extra, []byte("extra"))
		if _, err := Add(archive, []string{extra}); err != nil {
			t.Fatalf("update archive without times: %v", err)
		}
		command = exec.Command(upstream, "t", "-bd", archive)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("upstream test after update failed: %v\n%s", err, output)
		}
	})
}

func findUpstream7z(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"7z", "7zz", "7za"} {
		if executable, err := exec.LookPath(name); err == nil {
			return executable
		}
	}
	t.Skip("upstream 7-Zip is not installed")
	return ""
}

func mustWriteFile(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, name string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func TestMatches(t *testing.T) {
	for _, test := range []struct {
		name     string
		patterns []string
		want     bool
	}{
		{"dir/file.txt", nil, true},
		{"dir/file.txt", []string{"dir"}, true},
		{"dir/file.txt", []string{"*.txt"}, false},
		{"dir/file.txt", []string{"dir/*.txt"}, true},
		{"dir/file.txt", []string{"*"}, true},
		{"dir/file.txt", []string{"**/*.txt"}, true},
		{"one/two/file.txt", []string{"**/*.txt"}, false},
		{"literal[1].txt", []string{"literal[1].txt"}, true},
		{"literal1.txt", []string{"literal[1].txt"}, false},
		{"dir/file.txt", BuildPatterns([]string{"*.txt"}, nil, true), true},
		{"dir/file.txt", BuildPatterns([]string{"*"}, []string{"*.txt"}, true), false},
		{"dir/file.bin", BuildPatterns([]string{"*.txt"}, nil, true), false},
		{"dir/cache/file.txt", BuildPatterns(nil, []string{"dir/cache"}, false), false},
	} {
		got, err := matches(test.name, test.patterns)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("matches(%q, %q) = %v, want %v", test.name, test.patterns, got, test.want)
		}
	}
}
