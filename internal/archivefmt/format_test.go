package archivefmt

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := map[string]Format{
		"archive.7z":      Format7z,
		"archive.zip":     FormatZip,
		"archive.tar":     FormatTar,
		"archive.tar.gz":  FormatTarGzip,
		"archive.tgz":     FormatTarGzip,
		"archive.tar.bz2": FormatTarBzip2,
		"archive.tbz2":    FormatTarBzip2,
		"archive.tar.xz":  FormatTarXZ,
		"archive.txz":     FormatTarXZ,
		"archive.gz":      FormatGzip,
		"archive.bz2":     FormatBzip2,
		"archive.xz":      FormatXZ,
		"archive.zst":     FormatZstd,
		"archive.tar.zst": FormatTarZstd,
		"archive.iso":     FormatISO,
		"archive.udf":     FormatISO,
		"archive.wim":     FormatWIM,
		"archive.vhd":     FormatVHD,
		"archive.vhdx":    FormatVHDX,
	}
	for name, want := range tests {
		got, err := Resolve("", name)
		if err != nil {
			t.Errorf("Resolve(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, want %q", name, got, want)
		}
	}
	if _, err := Resolve("", "archive.unknown"); err == nil {
		t.Fatal("unknown extension unexpectedly resolved")
	}
	for explicit, want := range map[string]Format{
		"tar":   FormatTar,
		"gzip":  FormatGzip,
		"bzip2": FormatBzip2,
		"xz":    FormatXZ,
		"zstd":  FormatZstd,
		"iso":   FormatISO,
		"udf":   FormatISO,
		"wim":   FormatWIM,
		"vhd":   FormatVHD,
		"vhdx":  FormatVHDX,
	} {
		got, err := Resolve(explicit, "misleading.tar.gz")
		if err != nil {
			t.Errorf("Resolve(%q, misleading name): %v", explicit, err)
			continue
		}
		if got != want {
			t.Errorf("Resolve(%q, misleading name) = %q, want %q", explicit, got, want)
		}
	}
}

func TestResolveInputUsesSignature(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "misleading.zip")
	mustWrite(t, archive, append([]byte("MSWIM\x00\x00\x00"), make([]byte, 512)...))

	got, err := resolveInput("", archive)
	if err != nil {
		t.Fatal(err)
	}
	if got != FormatWIM {
		t.Fatalf("resolveInput() = %q, want %q", got, FormatWIM)
	}

	got, err = resolveInput("zip", archive)
	if err != nil {
		t.Fatal(err)
	}
	if got != FormatZip {
		t.Fatalf("resolveInput(explicit zip) = %q, want %q", got, FormatZip)
	}
}

func TestSingleStreamFormats(t *testing.T) {
	upstream := find7z(t)
	for _, extension := range []string{".gz", ".bz2", ".xz", ".zst"} {
		extension := extension
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "payload.txt")
			mustWrite(t, source, []byte("single stream payload"))
			archive := filepath.Join(root, "payload.txt"+extension)
			result, err := Add(archive, []string{source}, AddOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Files != 1 || result.Bytes != uint64(len("single stream payload")) {
				t.Fatalf("result = %#v", result)
			}
			if _, err := List(archive, "", "", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Test(archive, "", "", nil); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(root, "output")
			if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
				t.Fatal(err)
			}
			assertContent(t, filepath.Join(output, "payload.txt"), []byte("single stream payload"))
			command := exec.Command(upstream, "t", "-bd", archive)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("upstream test failed: %v\n%s", err, output)
			}

			mustWrite(t, source, []byte("replacement"))
			if _, err := Add(archive, []string{source}, AddOptions{}); err != nil {
				t.Fatal(err)
			}
			updated := filepath.Join(root, "updated")
			if _, err := Extract(archive, ExtractOptions{OutputDir: updated}); err != nil {
				t.Fatal(err)
			}
			assertContent(t, filepath.Join(updated, "payload.txt"), []byte("replacement"))
		})
	}
}

func TestPortableFormatsRoundTripAndUpdate(t *testing.T) {
	upstream := find7z(t)
	for _, extension := range []string{".zip", ".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"} {
		extension := extension
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			value := filepath.Join(root, "value.txt")
			keep := filepath.Join(root, "keep.txt")
			added := filepath.Join(root, "added.txt")
			mustWrite(t, value, []byte("old"))
			mustWrite(t, keep, []byte("keep"))
			archive := filepath.Join(root, "archive"+extension)
			result, err := Add(archive, []string{value, keep}, AddOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Files != 2 || result.Bytes != 7 {
				t.Fatalf("create result = %#v", result)
			}

			entries, err := List(archive, "", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name)
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, []string{"keep.txt", "value.txt"}) {
				t.Fatalf("names = %q", names)
			}
			tested, err := Test(archive, "", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tested.Files != 2 || tested.Bytes != 7 {
				t.Fatalf("test result = %#v", tested)
			}

			output := filepath.Join(root, "output")
			if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
				t.Fatal(err)
			}
			assertContent(t, filepath.Join(output, "value.txt"), []byte("old"))
			assertContent(t, filepath.Join(output, "keep.txt"), []byte("keep"))

			mustWrite(t, value, []byte("new"))
			mustWrite(t, added, []byte("added"))
			result, err = Add(archive, []string{value, added}, AddOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Files != 2 || result.Bytes != 8 {
				t.Fatalf("update result = %#v", result)
			}
			updated := filepath.Join(root, "updated")
			if _, err := Extract(archive, ExtractOptions{OutputDir: updated}); err != nil {
				t.Fatal(err)
			}
			assertContent(t, filepath.Join(updated, "value.txt"), []byte("new"))
			assertContent(t, filepath.Join(updated, "keep.txt"), []byte("keep"))
			assertContent(t, filepath.Join(updated, "added.txt"), []byte("added"))

			command := exec.Command(upstream, "t", "-bd", archive)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("upstream test failed: %v\n%s", err, output)
			}
		})
	}
}

func TestZipReadsUpstream(t *testing.T) {
	upstream := find7z(t)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "payload.txt"), []byte("upstream zip"))
	archive := filepath.Join(root, "upstream.zip")
	command := exec.Command(upstream, "a", "-bd", "-y", "-tzip", archive, "payload.txt")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("upstream ZIP create failed: %v\n%s", err, output)
	}
	result, err := Test(archive, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes != uint64(len("upstream zip")) {
		t.Fatalf("result = %#v", result)
	}
}

func TestZipCompressionTuning(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "payload.txt")
	mustWrite(t, source, bytes.Repeat([]byte("payload\n"), 256))
	for _, test := range []struct {
		name   string
		level  int
		method string
		want   uint16
	}{
		{name: "store", level: 0, want: zip.Store},
		{name: "deflate", level: 9, method: "deflate", want: zip.Deflate},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := filepath.Join(root, test.name+".zip")
			if _, err := Add(archive, []string{source}, AddOptions{
				Level:        test.level,
				LevelDefined: true,
				Method:       test.method,
			}); err != nil {
				t.Fatal(err)
			}
			reader, err := zip.OpenReader(archive)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if len(reader.File) != 1 || reader.File[0].Method != test.want {
				t.Fatalf("ZIP method = %v, want %v", reader.File[0].Method, test.want)
			}
		})
	}
}

func TestNon7zRejects7zOnlyOptions(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "value.txt")
	mustWrite(t, source, []byte("value"))
	archive := filepath.Join(root, "value.zip")
	for _, options := range []AddOptions{
		{Password: "secret"},
		{Solid: true, SolidDefined: true},
		{HeaderEncryption: true, Password: "secret"},
	} {
		if _, err := Add(archive, []string{source}, options); err == nil {
			t.Errorf("Add with %#v unexpectedly succeeded", options)
		}
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	for _, archiveType := range []string{"zip", "tar"} {
		archiveType := archiveType
		t.Run(archiveType, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "hostile."+archiveType)
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			if archiveType == "zip" {
				writer := zip.NewWriter(file)
				entry, createErr := writer.Create("../outside.txt")
				if createErr != nil {
					t.Fatal(createErr)
				}
				_, _ = entry.Write([]byte("outside"))
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				writer := tar.NewWriter(file)
				if err := writer.WriteHeader(&tar.Header{Name: "../outside.txt", Mode: 0o644, Size: 7}); err != nil {
					t.Fatal(err)
				}
				_, _ = writer.Write([]byte("outside"))
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(root, "output")
			if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err == nil {
				t.Fatal("hostile path unexpectedly extracted")
			}
			if _, err := os.Stat(filepath.Join(root, "outside.txt")); !os.IsNotExist(err) {
				t.Fatalf("outside path was created: %v", err)
			}
		})
	}
}

func TestExtractRejectsTarLink(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "link.tar")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	if err := writer.WriteHeader(&tar.Header{Name: "link", Linkname: "../outside", Typeflag: tar.TypeSymlink, Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(archive, ExtractOptions{OutputDir: filepath.Join(root, "output")}); err == nil {
		t.Fatal("symbolic link unexpectedly extracted")
	}
}

func find7z(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"7z", "7zz", "7za"} {
		if executable, err := exec.LookPath(name); err == nil {
			return executable
		}
	}
	t.Skip("upstream 7-Zip is not installed")
	return ""
}

func mustWrite(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, name string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
