package archive7z

import (
	"bytes"
	"encoding/binary"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableFilterArchiveInteroperability(t *testing.T) {
	for _, test := range []struct {
		name             string
		method           string
		password         string
		headerEncryption bool
		update           bool
		nonSolid         bool
	}{
		{name: "LZMA2", method: "lzma2"},
		{name: "LZMA", method: "lzma"},
		{name: "LZMA2_NonSolid", method: "lzma2", nonSolid: true},
		{
			name:             "LZMA2_AES_Update",
			method:           "lzma2",
			password:         "filter-password",
			headerEncryption: true,
			update:           true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents := executableFilterContents()
			root := t.TempDir()
			sources := writeExecutableFilterContents(t, root, contents)
			archive := filepath.Join(root, "filters.7z")
			options := AddOptions{
				Solid:            !test.nonSolid,
				Password:         test.password,
				HeaderEncryption: test.headerEncryption,
				Level:            1,
				LevelDefined:     true,
				Method:           test.method,
			}
			if _, err := AddWithOptions(archive, sources, options); err != nil {
				t.Fatal(err)
			}

			wantStreams := map[string]int{
				"plain.txt":   0,
				"arm64.sys":   1,
				"arm64ec.dll": 1,
				"x64.dll":     2,
				"x86.exe":     2,
				"ia64.exe":    3,
			}
			if test.nonSolid {
				wantStreams = map[string]int{
					"plain.txt":   0,
					"arm64.sys":   1,
					"arm64ec.dll": 2,
					"x64.dll":     3,
					"x86.exe":     4,
					"ia64.exe":    5,
				}
			}
			verifyExecutableFilterArchive(t, archive, test.password, contents, wantStreams)
			t.Run("UpstreamInitial", func(t *testing.T) {
				verifyUpstreamExecutableMethods(
					t, findUpstream7z(t), archive, test.password, test.method, contents, executableExpectedFilters(),
				)
			})

			if !test.update {
				return
			}
			updated := branchPE(0x014c, 4096, "")
			updated[900] ^= 0x5a
			contents["x86.exe"] = updated
			updatedPath := filepath.Join(root, "x86.exe")
			mustWriteFile(t, updatedPath, updated)
			if _, err := AddWithOptions(archive, []string{updatedPath}, options); err != nil {
				t.Fatalf("filtered encrypted update: %v", err)
			}
			for name := range wantStreams {
				wantStreams[name] = 0
			}
			wantStreams["x86.exe"] = 1
			verifyExecutableFilterArchive(t, archive, test.password, contents, wantStreams)
			t.Run("UpstreamUpdate", func(t *testing.T) {
				verifyUpstreamExecutableMethods(
					t, findUpstream7z(t), archive, test.password, test.method, contents,
					map[string]string{"x86.exe": "BCJ"},
				)
			})
		})
	}
}

func TestExecutableFiltersCanBeDisabled(t *testing.T) {
	for _, test := range []struct {
		name        string
		compression string
		options     AddOptions
	}{
		{
			name:        "ExplicitlyDisabled",
			compression: "lzma2",
			options: AddOptions{
				Solid: true, DisableFilters: true, Level: 1, LevelDefined: true, Method: "lzma2",
			},
		},
		{
			name:        "LevelZero",
			compression: "copy",
			options: AddOptions{
				Solid: true, Level: 0, LevelDefined: true,
			},
		},
		{
			name:        "CopyMethod",
			compression: "copy",
			options: AddOptions{
				Solid: true, Level: 7, LevelDefined: true, Method: "copy",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			contents := executableFilterContents()
			archive := filepath.Join(root, "unfiltered.7z")
			if _, err := AddWithOptions(
				archive, writeExecutableFilterContents(t, root, contents), test.options,
			); err != nil {
				t.Fatal(err)
			}
			wantStreams := make(map[string]int, len(contents))
			for name := range contents {
				wantStreams[name] = 0
			}
			verifyExecutableFilterArchive(t, archive, "", contents, wantStreams)
			t.Run("Upstream7Zip", func(t *testing.T) {
				verifyUpstreamExecutableMethods(
					t, findUpstream7z(t), archive, "", test.compression, contents, nil,
				)
			})
		})
	}
}

func executableFilterContents() map[string][]byte {
	return map[string][]byte{
		"plain.txt":   bytes.Repeat([]byte("plain filter control\n"), 128),
		"arm64.sys":   branchPE(0xaa64, 4096, ""),
		"arm64ec.dll": branchPE(0x8664, 4096, ".a64xrm"),
		"x64.dll":     branchPE(0x8664, 4096, ""),
		"x86.exe":     branchPE(0x014c, 4096, ""),
		"ia64.exe":    branchPE(0x0200, 4096, ""),
	}
}

func executableExpectedFilters() map[string]string {
	return map[string]string{
		"arm64.sys":   "ARM64",
		"arm64ec.dll": "ARM64",
		"x64.dll":     "BCJ",
		"x86.exe":     "BCJ",
		"ia64.exe":    "IA64",
	}
}

func writeExecutableFilterContents(t *testing.T, root string, contents map[string][]byte) []string {
	t.Helper()
	sources := make([]string, 0, len(contents))
	for name, content := range contents {
		path := filepath.Join(root, name)
		mustWriteFile(t, path, content)
		sources = append(sources, path)
	}
	return sources
}

func branchPE(machine uint16, size int, section string) []byte {
	payload := syntheticPE(machine, size, section)
	switch machine {
	case 0x014c, 0x8664:
		if section == ".a64xrm" {
			fillARM64Branches(payload)
			break
		}
		for offset := 768; offset+5 <= len(payload); offset += 37 {
			payload[offset] = 0xe8
			binary.LittleEndian.PutUint32(payload[offset+1:], uint32(offset*17))
		}
	case 0xaa64:
		fillARM64Branches(payload)
	case 0x0200:
		for bundle := 768; bundle+16 <= len(payload); bundle += 16 {
			payload[bundle] = 0x16
			for _, slot := range []int{1, 2} {
				offset := bundle + slot*5 - 4
				payload[offset] = 0
				binary.LittleEndian.PutUint32(payload[offset+1:], uint32(0x0a000000)<<slot)
			}
		}
	}
	return payload
}

func fillARM64Branches(payload []byte) {
	for offset := 768; offset+4 <= len(payload); offset += 4 {
		binary.LittleEndian.PutUint32(payload[offset:], 0x94000000)
	}
}

func verifyExecutableFilterArchive(
	t *testing.T,
	archive, password string,
	wantContents map[string][]byte,
	wantStreams map[string]int,
) {
	t.Helper()
	zr, err := openReader(archive, password)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	seen := make(map[string]bool, len(wantContents))
	for _, file := range zr.File {
		want, ok := wantContents[file.Name]
		if !ok {
			t.Fatalf("unexpected archive entry %q", file.Name)
		}
		stream, ok := wantStreams[file.Name]
		if !ok || file.Stream != stream {
			t.Fatalf("%s stream = %d, want %d", file.Name, file.Stream, stream)
		}
		r, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		got, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", file.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %s: %v", file.Name, closeErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("decoded content for %s differs from input", file.Name)
		}
		seen[file.Name] = true
	}
	if len(seen) != len(wantContents) {
		t.Fatalf("decoded %d files, want %d", len(seen), len(wantContents))
	}
}

func verifyUpstreamExecutableMethods(
	t *testing.T,
	upstream, archive, password, compression string,
	contents map[string][]byte,
	expectedFilters map[string]string,
) {
	t.Helper()
	args := []string{"t", "-bd"}
	if password != "" {
		args = append(args, "-p"+password)
	}
	args = append(args, archive)
	if output, err := exec.Command(upstream, args...).CombinedOutput(); err != nil {
		t.Fatalf("upstream test failed: %v\n%s", err, output)
	}

	args = []string{"l", "-bd", "-slt"}
	if password != "" {
		args = append(args, "-p"+password)
	}
	args = append(args, archive)
	output, err := exec.Command(upstream, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("upstream list failed: %v\n%s", err, output)
	}
	methods := parseUpstreamMethods(string(output))
	for name := range contents {
		method, ok := methods[name]
		if !ok {
			t.Fatalf("upstream listing has no method for %q:\n%s", name, output)
		}
		if !containsCompressionMethod(method, compression) {
			t.Fatalf("upstream method for %s = %q, want %s", name, method, compression)
		}
		filter := expectedFilters[name]
		if filter != "" && !strings.Contains(method, filter) {
			t.Fatalf("upstream method for %s = %q, want %s filter", name, method, filter)
		}
		if filter == "" && containsAnyExecutableFilter(method) {
			t.Fatalf("upstream method for %s unexpectedly contains a branch filter: %q", name, method)
		}
		if password != "" && !strings.Contains(method, "7zAES") {
			t.Fatalf("upstream method for %s = %q, want 7zAES", name, method)
		}
	}
}

func parseUpstreamMethods(output string) map[string]string {
	methods := make(map[string]string)
	currentPath := ""
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "Path = "):
			currentPath = strings.TrimPrefix(line, "Path = ")
		case currentPath != "" && strings.HasPrefix(line, "Method = "):
			methods[currentPath] = strings.TrimPrefix(line, "Method = ")
		}
	}
	return methods
}

func containsCompressionMethod(method, want string) bool {
	want = strings.ToUpper(want)
	for _, field := range strings.Fields(method) {
		name, _, _ := strings.Cut(field, ":")
		if strings.ToUpper(name) == want {
			return true
		}
	}
	return false
}

func containsAnyExecutableFilter(method string) bool {
	for _, filter := range []string{"ARM64", "BCJ", "IA64"} {
		if strings.Contains(method, filter) {
			return true
		}
	}
	return false
}
