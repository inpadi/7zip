package xpress

import (
	"bytes"
	"encoding/base64"
	"testing"
)

const compressedFixture = `fQIlAoeHCAAAAAiAeIAAAAAIAAAIBgAAAAAAZnh3B4gAAHaFcHB3AGdwaHcHZwYHAICAAGB2V3dnB1ZWh2ZlAGgAAAAAAAAAAAAIgAAIAAAAAAAAAIAIAABwAIAAAICAAIAAgAAABwAAAAAAAAgAAAAACACAAAAAgAAIgAAAAAAAAAAACIgAAAAAAAAAAAAAAAAAAAcAAAAAAAAAhoiAAAAAAACIhnAAAHAAAIeAgAAHAAAAdgCAAAAAAABoBoiGAAAAgHAAAAAAAACAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADdhXihy+0KhZAaOdszUWsh6HDv9N3mc5XnVnc23RVb2hxcOnEmN0hvz58/Z6tvk6/alNopptLnGpJWmVY0QAD00OFQkiqXRf2k83loZG8EImAaEYHGcqGqRaqzBs4Fi0lJJEI1iGkb4mGoiBM0YmwAbcmrqdQAzzEtPAMUtpY+yfoRog6ySgMYx0YBxCmfGfMs6YCVK4i5EJBMSWIl4kHSHdJjT95lOnHrUtak9wOIS036G3QCSMa4BPmaAPhLHCMwo/TCU/SfHyfqSGq9eQpVUwRvTChBQUo7HmDO/Y7AX//HpkcMEQMtisRjcIYh2CXh1pG3OtzSml8is1XAIDLVovm/RiQNF4EgdlxSt2I36dPExqNoBJnmPt4Y80B+KYoH2gBgAAA=`

func TestDecompress(t *testing.T) {
	fixture, err := base64.StdEncoding.DecodeString(compressedFixture)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decompress(fixture[4:], 638)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decoded, []byte(`<S>foo</S><S>bar</S>`)) {
		t.Fatal("decoded XPRESS payload does not contain the expected content")
	}
	if !bytes.HasSuffix(decoded, []byte(`</MS></Obj>`)) {
		t.Fatalf("unexpected XPRESS payload suffix: %q", decoded[len(decoded)-16:])
	}
}

func TestDecompressRejectsInvalidData(t *testing.T) {
	if _, err := Decompress(make([]byte, 260), 1); err == nil {
		t.Fatal("invalid Huffman table was accepted")
	}

	invalidDistance := make([]byte, 260)
	invalidDistance[0] = 1
	invalidDistance[128] = 1
	invalidDistance[257] = 0x80
	if _, err := Decompress(invalidDistance, 3); err == nil {
		t.Fatal("invalid match distance was accepted")
	}

	for length := 0; length < 260; length++ {
		if _, err := Decompress(make([]byte, length), 1); err == nil {
			t.Fatalf("truncated input of %d bytes was accepted", length)
		}
	}
}
