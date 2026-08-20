package archive7z

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inpadi/7zip/internal/sevenzip"
)

func TestClassifyPE(t *testing.T) {
	const (
		machineI386    = 0x014c
		machineAMD64   = 0x8664
		machineIA64    = 0x0200
		machineARM64   = 0xaa64
		machineARM64EC = 0xa641
		machineARM64X  = 0xa64e
	)
	tests := []struct {
		name    string
		size    int64
		want    sevenzip.BranchFilter
		mutate  func([]byte)
		section string
		machine uint16
	}{
		{name: "i386", machine: machineI386, size: 1024, want: sevenzip.BranchFilterBCJ},
		{name: "amd64", machine: machineAMD64, size: 1024, want: sevenzip.BranchFilterBCJ},
		{name: "arm64", machine: machineARM64, size: 1024, want: sevenzip.BranchFilterARM64},
		{name: "arm64ec machine", machine: machineARM64EC, size: 1024, want: sevenzip.BranchFilterARM64},
		{name: "arm64x machine", machine: machineARM64X, size: 1024, want: sevenzip.BranchFilterARM64},
		{name: "arm64ec", machine: machineAMD64, section: ".a64xrm", size: 1024, want: sevenzip.BranchFilterARM64},
		{name: "arm64ec machine", machine: 0xa641, size: 1024, want: sevenzip.BranchFilterARM64},
		{name: "ia64", machine: machineIA64, size: 1024, want: sevenzip.BranchFilterIA64},
		{name: "unknown machine", machine: 0x01f0, size: 1024},
		{name: "arm64 unaligned size", machine: machineARM64, size: 1025},
		{name: "arm64 negative size", machine: machineARM64, size: -1},
		{name: "ia64 unaligned size", machine: machineIA64, size: 1032},
		{
			name: "bad DOS signature", machine: machineI386, size: 1024,
			mutate: func(header []byte) { header[0] = 0 },
		},
		{
			name: "unaligned PE offset", machine: machineI386, size: 1024,
			mutate: func(header []byte) { binary.LittleEndian.PutUint32(header[0x3c:], 0x81) },
		},
		{
			name: "bad PE signature", machine: machineI386, size: 1024,
			mutate: func(header []byte) { header[0x80] = 0 },
		},
		{
			name: "PE offset beyond analysis window", machine: machineI386, size: 8192,
			mutate: func(header []byte) { binary.LittleEndian.PutUint32(header[0x3c:], 0x1000) },
		},
		{
			name: "PE offset wraps int32", machine: machineI386, size: 1024,
			mutate: func(header []byte) { binary.LittleEndian.PutUint32(header[0x3c:], 0xfffffff8) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := syntheticPE(test.machine, 1024, test.section)
			if test.mutate != nil {
				test.mutate(header)
			}
			if got := classifyPE(header, test.size); got != test.want {
				t.Fatalf("classifyPE() = %d, want %d", got, test.want)
			}
		})
	}

	t.Run("short header", func(t *testing.T) {
		if got := classifyPE(syntheticPE(machineI386, 511, ""), 511); got != sevenzip.BranchFilterNone {
			t.Fatalf("classifyPE(short header) = %d, want none", got)
		}
	})
	t.Run("truncated PE window", func(t *testing.T) {
		header := syntheticPE(machineI386, 1024, "")
		binary.LittleEndian.PutUint32(header[0x3c:], 0x300)
		if got := classifyPE(header, int64(len(header))); got != sevenzip.BranchFilterNone {
			t.Fatalf("classifyPE(truncated PE window) = %d, want none", got)
		}
	})
}

func TestPlanInputGroupsRejectsExecutableReplacement(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "driver.exe")
	replacement := filepath.Join(root, "replacement.exe")
	mustWriteFile(t, input, syntheticPE(0x014c, 1024, ""))
	mustWriteFile(t, replacement, syntheticPE(0xaa64, 1024, ""))
	inputs, roots, err := collectInputs([]string{input}, filepath.Join(root, "output.7z"), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInputRoots(roots)
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(replacement, input); err != nil {
		t.Fatal(err)
	}

	_, _, err = planInputGroups(inputs, true)
	if err == nil {
		t.Fatal("executable replaced after enumeration was accepted by the analysis scan")
	}
	if !strings.Contains(err.Error(), "analyze") || !strings.Contains(err.Error(), "changed after it was enumerated") {
		t.Fatalf("replacement error = %q, want analysis identity error", err)
	}
}

func TestExecutableCandidate(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "driver.SYS", want: true},
		{name: "program.exe", want: true},
		{name: "library.DlL", want: true},
		{name: "control.ocx", want: true},
		{name: "installer.sfx", want: true},
		{name: "renamed.bin"},
		{name: "extensionless"},
		{name: ".exe", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := executableCandidate(test.name); got != test.want {
				t.Fatalf("executableCandidate(%q) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}

func TestPlanInputGroups(t *testing.T) {
	root := t.TempDir()
	contents := map[string][]byte{
		"arm64.SYS":   syntheticPE(0xaa64, 1024, ""),
		"arm64ec.dll": syntheticPE(0x8664, 1028, ".a64xrm"),
		"ia64.exe":    syntheticPE(0x0200, 1040, ""),
		"plain.txt":   []byte("plain data"),
		"renamed.bin": syntheticPE(0x014c, 1024, ""),
		"x64.DLL":     syntheticPE(0x8664, 1030, ""),
		"x86.exe":     syntheticPE(0x014c, 1031, ""),
	}
	sources := make([]string, 0, len(contents))
	for name, content := range contents {
		path := filepath.Join(root, name)
		mustWriteFile(t, path, content)
		sources = append(sources, path)
	}
	inputs, roots, err := collectInputs(sources, filepath.Join(root, "output.7z"), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInputRoots(roots)

	directories, groups, err := planInputGroups(inputs, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 0 {
		t.Fatalf("directories = %d, want 0", len(directories))
	}
	want := []struct {
		filter sevenzip.BranchFilter
		names  []string
		size   uint64
	}{
		{filter: sevenzip.BranchFilterNone, names: []string{"plain.txt", "renamed.bin"}, size: 10 + 1024},
		{filter: sevenzip.BranchFilterARM64, names: []string{"arm64.SYS", "arm64ec.dll"}, size: 1024 + 1028},
		{filter: sevenzip.BranchFilterBCJ, names: []string{"x64.DLL", "x86.exe"}, size: 1030 + 1031},
		{filter: sevenzip.BranchFilterIA64, names: []string{"ia64.exe"}, size: 1040},
	}
	if len(groups) != len(want) {
		t.Fatalf("groups = %d, want %d", len(groups), len(want))
	}
	for i, group := range groups {
		if group.filter != want[i].filter || group.expectedSize != want[i].size {
			t.Fatalf("group %d = {filter:%d size:%d}, want {filter:%d size:%d}",
				i, group.filter, group.expectedSize, want[i].filter, want[i].size)
		}
		names := make([]string, len(group.files))
		for j, file := range group.files {
			names[j] = file.name
		}
		if !reflect.DeepEqual(names, want[i].names) {
			t.Fatalf("group %d names = %q, want %q", i, names, want[i].names)
		}
	}

	_, disabled, err := planInputGroups(inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 1 || disabled[0].filter != sevenzip.BranchFilterNone ||
		len(disabled[0].files) != len(inputs) {
		t.Fatalf("disabled groups = %#v, want one unfiltered group containing every input", disabled)
	}
}

func TestExecutableFiltersEnabled(t *testing.T) {
	for _, test := range []struct {
		name    string
		options AddOptions
		level   int
		want    bool
	}{
		{name: "default", level: -1, want: true},
		{name: "lzma", options: AddOptions{Method: "LZMA"}, level: 7, want: true},
		{name: "lzma2", options: AddOptions{Method: "lzma2"}, level: 7, want: true},
		{name: "disabled", options: AddOptions{DisableFilters: true}, level: 7},
		{name: "level zero", level: 0},
		{name: "copy", options: AddOptions{Method: "copy"}, level: 7},
		{name: "store", options: AddOptions{Method: "store"}, level: 7},
		{name: "unknown", options: AddOptions{Method: "unknown"}, level: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := executableFiltersEnabled(test.options, test.level); got != test.want {
				t.Fatalf("executableFiltersEnabled() = %t, want %t", got, test.want)
			}
		})
	}
}

func syntheticPE(machine uint16, size int, section string) []byte {
	const peOffset = 0x80
	header := make([]byte, size)
	if len(header) < 0x40 {
		return header
	}
	header[0], header[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(header[0x3c:], peOffset)
	if len(header) < peOffset+24 {
		return header
	}
	copy(header[peOffset:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(header[peOffset+4:], machine)
	if section != "" {
		binary.LittleEndian.PutUint16(header[peOffset+6:], 1)
		copy(header[peOffset+24:], section)
	}
	return header
}
