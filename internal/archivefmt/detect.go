package archivefmt

import (
	"bytes"
	"io"
	"os"
)

const signatureProbeSize = 16*2048 + 6

// resolveInput uses strong archive signatures when no type was explicitly set.
// Extensions remain useful for formats whose outer compression stream does not
// reveal whether its payload is a tar archive.
func resolveInput(explicit, archive string) (Format, error) {
	if explicit != "" {
		return Resolve(explicit, archive)
	}

	inferred, extensionErr := Resolve("", archive)
	detected, ok, err := detectFormat(archive)
	if err != nil {
		if extensionErr == nil {
			return inferred, nil
		}
		return "", err
	}
	if ok {
		if matchingTarStream(inferred, detected) {
			return inferred, nil
		}
		return detected, nil
	}
	if extensionErr != nil {
		return "", extensionErr
	}
	return inferred, nil
}

func matchingTarStream(inferred, detected Format) bool {
	return (inferred == FormatTarGzip && detected == FormatGzip) ||
		(inferred == FormatTarBzip2 && detected == FormatBzip2) ||
		(inferred == FormatTarXZ && detected == FormatXZ) ||
		(inferred == FormatTarZstd && detected == FormatZstd)
}

func detectFormat(name string) (Format, bool, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", false, err
	}
	defer file.Close()

	header := make([]byte, signatureProbeSize)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false, err
	}
	header = header[:n]

	switch {
	case hasPrefix(header, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		return Format7z, true, nil
	case hasPrefix(header, []byte("MSWIM\x00\x00\x00")):
		return FormatWIM, true, nil
	case hasPrefix(header, []byte("vhdxfile")):
		return FormatVHDX, true, nil
	case hasPrefix(header, []byte{'P', 'K', 3, 4}),
		hasPrefix(header, []byte{'P', 'K', 5, 6}),
		hasPrefix(header, []byte{'P', 'K', 7, 8}):
		return FormatZip, true, nil
	case hasPrefix(header, []byte{0x1f, 0x8b}):
		return FormatGzip, true, nil
	case hasPrefix(header, []byte("BZh")):
		return FormatBzip2, true, nil
	case hasPrefix(header, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return FormatXZ, true, nil
	case hasPrefix(header, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return FormatZstd, true, nil
	case len(header) >= 262 && bytes.Equal(header[257:262], []byte("ustar")):
		return FormatTar, true, nil
	case len(header) >= signatureProbeSize && bytes.Equal(header[16*2048+1:16*2048+6], []byte("CD001")):
		return FormatISO, true, nil
	case len(header) >= signatureProbeSize && bytes.Equal(header[16*2048+1:16*2048+6], []byte("BEA01")):
		return FormatISO, true, nil
	}

	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	if info.Size() >= 512 {
		footer := make([]byte, 8)
		if _, err := file.ReadAt(footer, info.Size()-512); err != nil {
			return "", false, err
		}
		if bytes.Equal(footer, []byte("conectix")) {
			return FormatVHD, true, nil
		}
	}
	return "", false, nil
}

func hasPrefix(data, prefix []byte) bool {
	return len(data) >= len(prefix) && bytes.Equal(data[:len(prefix)], prefix)
}
