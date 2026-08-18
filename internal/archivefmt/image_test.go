package archivefmt

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestISO9660RoundTrip(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "sample.iso")
	createTestISO(t, archive, []byte("ISO payload"))
	entries, err := List(archive, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "HELLO.TXT" || entries[0].Size != uint64(len("ISO payload")) {
		t.Fatalf("ISO entries = %#v", entries)
	}
	if _, err := Test(archive, "", "", nil); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(output, "HELLO.TXT"), []byte("ISO payload"))
	upstream := find7z(t)
	command := exec.Command(upstream, "t", "-bd", archive)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("upstream ISO test failed: %v\n%s", err, output)
	}
}

func TestUDFPreferredOverISO9660Bridge(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "hybrid.iso")
	payload := []byte("UDF payload")
	createTestHybridUDF(t, archive, payload)

	entries, err := List(archive, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "payload.txt" || entries[0].Size != uint64(len(payload)) {
		t.Fatalf("UDF entries = %#v", entries)
	}
	if _, err := Test(archive, "", "", nil); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(output, "payload.txt"), payload)
}

func TestWIMReadsUpstream(t *testing.T) {
	upstream := find7z(t)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "payload.txt"), []byte("WIM payload"))
	archive := filepath.Join(root, "sample.wim")
	command := exec.Command(upstream, "a", "-bd", "-y", "-twim", archive, "payload.txt")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("upstream WIM create failed: %v\n%s", err, output)
	}
	entries, err := List(archive, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "payload.txt" {
		t.Fatalf("WIM entries = %#v", entries)
	}
	if _, err := Test(archive, "", "", nil); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(output, "payload.txt"), []byte("WIM payload"))

	misleading := filepath.Join(root, "sample.zip")
	if err := os.Rename(archive, misleading); err != nil {
		t.Fatal(err)
	}
	output = filepath.Join(root, "misleading-output")
	if _, err := Extract(misleading, ExtractOptions{OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(output, "payload.txt"), []byte("WIM payload"))
}

func TestFixedVHDRoundTrip(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "sample.vhd")
	payload := createTestVHD(t, archive)
	testVirtualDisk(t, archive, payload)
}

func TestFixedVHDXRoundTrip(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "sample.vhdx")
	payload := createTestVHDX(t, archive)
	testVirtualDisk(t, archive, payload)
}

func TestImageReadersRejectCorruptHeaders(t *testing.T) {
	root := t.TempDir()

	vhd := filepath.Join(root, "corrupt.vhd")
	createTestVHD(t, vhd)
	content, err := os.ReadFile(vhd)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)-1] ^= 0xff
	if err := os.WriteFile(vhd, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(vhd, "", "", nil); err == nil {
		t.Fatal("VHD with an invalid footer checksum was accepted")
	}

	vhdx := filepath.Join(root, "corrupt.vhdx")
	createTestVHDX(t, vhdx)
	content, err = os.ReadFile(vhdx)
	if err != nil {
		t.Fatal(err)
	}
	content[64*1024+100] ^= 0xff
	content[128*1024+100] ^= 0xff
	if err := os.WriteFile(vhdx, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(vhdx, "", "", nil); err == nil {
		t.Fatal("VHDX with invalid header checksums was accepted")
	}

	for _, extension := range []string{".iso", ".wim"} {
		name := filepath.Join(root, "invalid"+extension)
		if err := os.WriteFile(name, []byte("not an image"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := List(name, "", "", nil); err == nil {
			t.Fatalf("invalid %s image was accepted", extension)
		}
	}
}

func testVirtualDisk(t *testing.T, archive string, payload []byte) {
	t.Helper()
	entries, err := List(archive, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "0.img" || entries[0].Size != uint64(len(payload)) {
		t.Fatalf("virtual disk entries = %#v", entries)
	}
	if _, err := Test(archive, "", "", nil); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(filepath.Dir(archive), "output-"+filepath.Ext(archive)[1:])
	if _, err := Extract(archive, ExtractOptions{OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(output, "0.img"), payload)
	upstream := find7z(t)
	command := exec.Command(upstream, "t", "-bd", archive)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("upstream virtual disk test failed: %v\n%s", err, output)
	}
}

func createTestISO(t *testing.T, name string, payload []byte) {
	t.Helper()
	const sectors = 22
	image := make([]byte, sectors*isoSectorSize)
	pvd := image[16*isoSectorSize : 17*isoSectorSize]
	pvd[0], pvd[6] = 1, 1
	copy(pvd[1:6], "CD001")
	copy(pvd[8:40], padISO("GO7ZIP", 32))
	copy(pvd[40:72], padISO("GO7ZIP_TEST", 32))
	putBoth32(pvd[80:88], sectors)
	putBoth16(pvd[120:124], 1)
	putBoth16(pvd[124:128], 1)
	putBoth16(pvd[128:132], isoSectorSize)
	putBoth32(pvd[132:140], 10)
	binary.LittleEndian.PutUint32(pvd[140:144], 18)
	binary.BigEndian.PutUint32(pvd[148:152], 19)
	writeISORecord(pvd[156:], 20, isoSectorSize, 2, []byte{0})
	copy(pvd[813:830], "2026081812000000")
	pvd[830] = 0
	copy(pvd[830:847], "2026081812000000")
	pvd[847] = 0
	pvd[881] = 1
	terminator := image[17*isoSectorSize : 18*isoSectorSize]
	terminator[0], terminator[6] = 255, 1
	copy(terminator[1:6], "CD001")
	writePathTable(image[18*isoSectorSize:], 20, binary.LittleEndian)
	writePathTable(image[19*isoSectorSize:], 20, binary.BigEndian)
	directory := image[20*isoSectorSize : 21*isoSectorSize]
	offset := writeISORecord(directory, 20, isoSectorSize, 2, []byte{0})
	offset += writeISORecord(directory[offset:], 20, isoSectorSize, 2, []byte{1})
	writeISORecord(directory[offset:], 21, len(payload), 0, []byte("HELLO.TXT;1"))
	copy(image[21*isoSectorSize:], payload)
	if err := os.WriteFile(name, image, 0o600); err != nil {
		t.Fatal(err)
	}
}

func createTestHybridUDF(t *testing.T, name string, payload []byte) {
	t.Helper()
	const (
		sectors        = 310
		partitionStart = 300
	)
	image := make([]byte, sectors*isoSectorSize)

	// Minimal ISO9660 bridge containing a different file.
	pvd := image[16*isoSectorSize : 17*isoSectorSize]
	pvd[0], pvd[6] = 1, 1
	copy(pvd[1:6], "CD001")
	putBoth32(pvd[80:88], sectors)
	putBoth16(pvd[128:132], isoSectorSize)
	writeISORecord(pvd[156:], 30, isoSectorSize, 2, []byte{0})
	terminator := image[17*isoSectorSize : 18*isoSectorSize]
	terminator[0], terminator[6] = 255, 1
	copy(terminator[1:6], "CD001")
	bridgeDirectory := image[30*isoSectorSize : 31*isoSectorSize]
	offset := writeISORecord(bridgeDirectory, 30, isoSectorSize, 2, []byte{0})
	offset += writeISORecord(bridgeDirectory[offset:], 30, isoSectorSize, 2, []byte{1})
	writeISORecord(bridgeDirectory[offset:], 31, len("bridge"), 0, []byte("BRIDGE.TXT;1"))
	copy(image[31*isoSectorSize:], "bridge")

	for sector, signature := range map[int]string{18: "BEA01", 19: "NSR02", 20: "TEA01"} {
		copy(image[sector*isoSectorSize+1:], signature)
	}
	anchor := image[256*isoSectorSize : 257*isoSectorSize]
	writeUDFTag(anchor, 2, 256)
	binary.LittleEndian.PutUint32(anchor[16:20], 3*isoSectorSize)
	binary.LittleEndian.PutUint32(anchor[20:24], 257)

	partition := image[257*isoSectorSize : 258*isoSectorSize]
	writeUDFTag(partition, 5, 257)
	binary.LittleEndian.PutUint32(partition[188:192], partitionStart)
	binary.LittleEndian.PutUint32(partition[192:196], sectors-partitionStart)
	logical := image[258*isoSectorSize : 259*isoSectorSize]
	writeUDFTag(logical, 6, 258)
	binary.LittleEndian.PutUint32(logical[252:256], 0)
	writeUDFTag(image[259*isoSectorSize:260*isoSectorSize], 8, 259)

	fileSet := image[partitionStart*isoSectorSize : (partitionStart+1)*isoSectorSize]
	writeUDFTag(fileSet, 256, partitionStart)
	binary.LittleEndian.PutUint32(fileSet[404:408], 1)

	fileIdentifier := append([]byte{8}, []byte("payload.txt")...)
	directoryLength := 38 + len(fileIdentifier)
	if directoryLength%4 != 0 {
		directoryLength += 4 - directoryLength%4
	}
	writeTestUDFFileEntry(image[(partitionStart+1)*isoSectorSize:], true, uint64(directoryLength), uint32(directoryLength), 2)
	directory := image[(partitionStart+2)*isoSectorSize : (partitionStart+3)*isoSectorSize]
	writeUDFTag(directory, 257, partitionStart+2)
	directory[19] = byte(len(fileIdentifier))
	binary.LittleEndian.PutUint32(directory[24:28], 3)
	copy(directory[38:], fileIdentifier)

	writeTestUDFFileEntry(image[(partitionStart+3)*isoSectorSize:], false, uint64(len(payload)), uint32(len(payload)), 4)
	copy(image[(partitionStart+4)*isoSectorSize:], payload)

	if err := os.WriteFile(name, image, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeUDFTag(sector []byte, identifier uint16, location uint32) {
	binary.LittleEndian.PutUint16(sector[0:2], identifier)
	binary.LittleEndian.PutUint16(sector[2:4], 2)
	binary.LittleEndian.PutUint32(sector[10:14], location)
	var checksum byte
	for i := 0; i < 16; i++ {
		if i != 4 {
			checksum += sector[i]
		}
	}
	sector[4] = checksum
}

func writeTestUDFFileEntry(sector []byte, directory bool, informationLength uint64, extentLength, extentPosition uint32) {
	writeUDFTag(sector, 260, 0)
	sector[27] = 5
	if directory {
		sector[27] = 4
	}
	binary.LittleEndian.PutUint32(sector[44:48], 0x14a5)
	binary.LittleEndian.PutUint64(sector[56:64], informationLength)
	timestamp := sector[84:96]
	binary.LittleEndian.PutUint16(timestamp[0:2], 1<<12)
	binary.LittleEndian.PutUint16(timestamp[2:4], 2026)
	timestamp[4], timestamp[5], timestamp[6] = 6, 13, 2
	binary.LittleEndian.PutUint32(sector[168:172], 0)
	binary.LittleEndian.PutUint32(sector[172:176], 8)
	binary.LittleEndian.PutUint32(sector[176:180], extentLength)
	binary.LittleEndian.PutUint32(sector[180:184], extentPosition)
}

func padISO(value string, size int) []byte {
	result := bytes.Repeat([]byte{' '}, size)
	copy(result, value)
	return result
}

func putBoth16(dst []byte, value uint16) {
	binary.LittleEndian.PutUint16(dst, value)
	binary.BigEndian.PutUint16(dst[2:], value)
}

func putBoth32(dst []byte, value uint32) {
	binary.LittleEndian.PutUint32(dst, value)
	binary.BigEndian.PutUint32(dst[4:], value)
}

func writeISORecord(dst []byte, extent, size int, flags byte, identifier []byte) int {
	length := 33 + len(identifier)
	if len(identifier)%2 == 0 {
		length++
	}
	dst[0], dst[1] = byte(length), 0
	putBoth32(dst[2:10], uint32(extent))
	putBoth32(dst[10:18], uint32(size))
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	dst[18], dst[19], dst[20] = byte(now.Year()-1900), byte(now.Month()), byte(now.Day())
	dst[21], dst[22], dst[23], dst[24] = byte(now.Hour()), byte(now.Minute()), byte(now.Second()), 0
	dst[25], dst[26], dst[27] = flags, 0, 0
	putBoth16(dst[28:32], 1)
	dst[32] = byte(len(identifier))
	copy(dst[33:], identifier)
	return length
}

func writePathTable(dst []byte, extent uint32, order binary.ByteOrder) {
	dst[0], dst[1] = 1, 0
	order.PutUint32(dst[2:6], extent)
	order.PutUint16(dst[6:8], 1)
	dst[8], dst[9] = 0, 0
}

func createTestVHD(t *testing.T, name string) []byte {
	t.Helper()
	payload := make([]byte, 1<<20)
	copy(payload, "fixed VHD payload")
	footer := make([]byte, 512)
	copy(footer, "conectix")
	binary.BigEndian.PutUint32(footer[8:12], 2)
	binary.BigEndian.PutUint32(footer[12:16], 0x00010000)
	binary.BigEndian.PutUint64(footer[16:24], ^uint64(0))
	binary.BigEndian.PutUint32(footer[24:28], uint32(time.Now().Unix()-946684800))
	copy(footer[28:32], "go7z")
	binary.BigEndian.PutUint32(footer[32:36], 0x00010000)
	copy(footer[36:40], "Wi2k")
	binary.BigEndian.PutUint64(footer[40:48], uint64(len(payload)))
	binary.BigEndian.PutUint64(footer[48:56], uint64(len(payload)))
	binary.BigEndian.PutUint32(footer[56:60], 0x00040811)
	binary.BigEndian.PutUint32(footer[60:64], 2)
	copy(footer[68:84], []byte("go7zip-fixed-vhd"))
	var sum uint32
	for _, value := range footer {
		sum += uint32(value)
	}
	binary.BigEndian.PutUint32(footer[64:68], ^sum)
	if err := os.WriteFile(name, append(payload, footer...), 0o600); err != nil {
		t.Fatal(err)
	}
	return payload
}

func createTestVHDX(t *testing.T, name string) []byte {
	t.Helper()
	const mb = 1 << 20
	image := make([]byte, 5*mb)
	payload := image[4*mb : 5*mb]
	copy(payload, "fixed VHDX payload")
	copy(image[0:8], "vhdxfile")
	creator := utf16LE("go7zip test")
	copy(image[8:520], creator)
	writeVHDXHeader(image[64*1024:128*1024], 1)
	writeVHDXHeader(image[128*1024:192*1024], 2)
	writeVHDXRegions(image[192*1024 : 256*1024])
	writeVHDXRegions(image[256*1024 : 320*1024])
	writeVHDXMetadata(image[2*mb : 3*mb])
	binary.LittleEndian.PutUint64(image[3*mb:3*mb+8], uint64(4<<20)|6)
	if err := os.WriteFile(name, image, 0o600); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), payload...)
}

func writeVHDXHeader(region []byte, sequence uint64) {
	copy(region[0:4], "head")
	binary.LittleEndian.PutUint64(region[8:16], sequence)
	copy(region[16:32], guidLE(uuid.MustParse("10203040-5060-7080-90a0-b0c0d0e0f001")))
	copy(region[32:48], guidLE(uuid.MustParse("11223344-5566-7788-99aa-bbccddeeff00")))
	binary.LittleEndian.PutUint16(region[64:66], 0)
	binary.LittleEndian.PutUint16(region[66:68], 1)
	binary.LittleEndian.PutUint32(region[68:72], 1<<20)
	binary.LittleEndian.PutUint64(region[72:80], 1<<20)
	putVHDXChecksum(region[:4096])
}

func writeVHDXRegions(region []byte) {
	copy(region[0:4], "regi")
	binary.LittleEndian.PutUint32(region[8:12], 2)
	writeVHDXRegionEntry(region[16:48], uuid.MustParse("2DC27766-F623-4200-9D64-115E9BFD4A08"), 3<<20, 1<<20)
	writeVHDXRegionEntry(region[48:80], uuid.MustParse("8B7CA206-4790-4B9A-B8FE-575F050F886E"), 2<<20, 1<<20)
	putVHDXChecksum(region)
}

func writeVHDXRegionEntry(dst []byte, id uuid.UUID, offset, length uint32) {
	copy(dst[0:16], guidLE(id))
	binary.LittleEndian.PutUint64(dst[16:24], uint64(offset))
	binary.LittleEndian.PutUint32(dst[24:28], length)
	binary.LittleEndian.PutUint32(dst[28:32], 1)
}

func writeVHDXMetadata(region []byte) {
	copy(region[0:8], "metadata")
	binary.LittleEndian.PutUint16(region[10:12], 5)
	type metadata struct {
		id     string
		offset uint32
		data   []byte
	}
	values := []metadata{
		{"CAA16737-FA36-4D43-B3B6-33F0AA44E76B", 65536, littleBytes(uint32(1<<20), uint32(1))},
		{"2FA54224-CD1B-4876-B211-5DBED83BF4B8", 65544, littleBytes(uint64(1 << 20))},
		{"BECA12AB-B2E6-4523-93EF-C309E000C746", 65552, guidLE(uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff"))},
		{"8141BF1D-A96F-4709-BA47-F233A8FAAB5F", 65568, littleBytes(uint32(512))},
		{"CDA348C7-445D-4471-9CC9-E9885251C556", 65572, littleBytes(uint32(512))},
	}
	for i, value := range values {
		entry := region[32+i*32 : 64+i*32]
		copy(entry[0:16], guidLE(uuid.MustParse(value.id)))
		binary.LittleEndian.PutUint32(entry[16:20], value.offset)
		binary.LittleEndian.PutUint32(entry[20:24], uint32(len(value.data)))
		entry[24] = 6
		if i == 0 {
			entry[24] = 4
		}
		copy(region[value.offset:], value.data)
	}
}

func littleBytes(values ...any) []byte {
	var out bytes.Buffer
	for _, value := range values {
		_ = binary.Write(&out, binary.LittleEndian, value)
	}
	return out.Bytes()
}

func utf16LE(value string) []byte {
	var result []byte
	for _, r := range value {
		result = binary.LittleEndian.AppendUint16(result, uint16(r))
	}
	return result
}

func guidLE(id uuid.UUID) []byte {
	result := append([]byte(nil), id[:]...)
	reverse(result[0:4])
	reverse(result[4:6])
	reverse(result[6:8])
	return result
}

func reverse(value []byte) {
	for i, j := 0, len(value)-1; i < j; i, j = i+1, j-1 {
		value[i], value[j] = value[j], value[i]
	}
}

func putVHDXChecksum(region []byte) {
	for i := 4; i < 8; i++ {
		region[i] = 0
	}
	checksum := crc32.Checksum(region, crc32.MakeTable(crc32.Castagnoli))
	binary.LittleEndian.PutUint32(region[4:8], checksum)
}
