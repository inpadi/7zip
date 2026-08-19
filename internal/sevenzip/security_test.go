package sevenzip

import (
	"testing"

	"github.com/inpadi/7zip/internal/security"
)

func TestMetadataItemLimit(t *testing.T) {
	if err := checkUint64(security.MaxMetadataItems+1, true); err == nil {
		t.Fatal("expected oversized metadata count to be rejected")
	}
}

func TestStreamsInfoRejectsMismatchedPackedStreams(t *testing.T) {
	streams := &streamsInfo{
		packInfo: &packInfo{streams: 1},
		unpackInfo: &unpackInfo{folder: []*folder{{
			coder:         []*coder{{in: 1, out: 1}},
			packedStreams: 1,
			packed:        []uint64{0},
			size:          []uint64{1},
		}}},
	}
	if err := streams.validate(); err == nil {
		t.Fatal("expected missing packed size to be rejected")
	}
}

func TestFileFolderAndSizeRejectsOutOfRangeIndex(t *testing.T) {
	streams := &streamsInfo{unpackInfo: &unpackInfo{folder: []*folder{{coder: []*coder{{}}, size: []uint64{1}}}}}
	if _, _, _, err := streams.FileFolderAndSize(1); err == nil {
		t.Fatal("expected invalid file stream index to be rejected")
	}
}
