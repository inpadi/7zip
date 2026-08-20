package archive7z

import (
	"bytes"
	"testing"

	"github.com/inpadi/7zip/internal/sevenzip"
)

func TestFilteredFolderCoderGraph(t *testing.T) {
	for _, filter := range []struct {
		name   string
		method []byte
	}{
		{name: "ARM64", method: []byte{0x0a}},
		{name: "BCJ", method: []byte{0x03, 0x03, 0x01, 0x03}},
		{name: "IA64", method: []byte{0x03, 0x03, 0x04, 0x01}},
	} {
		for _, encrypted := range []bool{false, true} {
			name := filter.name
			if encrypted {
				name += "_AES"
			}
			t.Run(name, func(t *testing.T) {
				folder := &writerFolder{
					method:       append([]byte(nil), methodLZMA2...),
					properties:   []byte{0x18},
					filterMethod: append([]byte(nil), filter.method...),
				}
				want := make([]byte, 0, 32)
				if encrypted {
					folder.aesProperties = []byte{0xaa, 0xbb}
					want = append(want,
						3,
						0x24, 0x06, 0xf1, 0x07, 0x01, 2, 0xaa, 0xbb,
					)
				} else {
					want = append(want, 2)
				}
				want = append(want, 0x21, 0x21, 1, 0x18)
				want = append(want, byte(len(filter.method)))
				want = append(want, filter.method...)
				if encrypted {
					want = append(want, 1, 0) // LZMA2 input <- AES output.
					want = append(want, 2, 1) // Filter input <- LZMA2 output.
				} else {
					want = append(want, 1, 0) // Filter input <- LZMA2 output.
				}

				var got bytes.Buffer
				writeFolder(&got, folder)
				if !bytes.Equal(got.Bytes(), want) {
					t.Fatalf("folder graph = % x, want % x", got.Bytes(), want)
				}
			})
		}
	}
}

func TestBranchFilterMethodIDs(t *testing.T) {
	for _, test := range []struct {
		filter sevenzip.BranchFilter
		want   []byte
	}{
		{filter: sevenzip.BranchFilterNone},
		{filter: sevenzip.BranchFilterARM64, want: []byte{0x0a}},
		{filter: sevenzip.BranchFilterBCJ, want: []byte{0x03, 0x03, 0x01, 0x03}},
		{filter: sevenzip.BranchFilterIA64, want: []byte{0x03, 0x03, 0x04, 0x01}},
	} {
		if got := branchFilterMethod(test.filter); !bytes.Equal(got, test.want) {
			t.Fatalf("branchFilterMethod(%d) = % x, want % x", test.filter, got, test.want)
		}
	}
}
