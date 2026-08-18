package archive7z

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"time"
	"unicode/utf16"

	"github.com/ulikunitz/xz/lzma"
)

const (
	idEnd = iota
	idHeader
	_
	_
	idMainStreamsInfo
	idFilesInfo
	idPackInfo
	idUnpackInfo
	idSubStreamsInfo
	idSize
	idCRC
	idFolder
	idCodersUnpackSize
	idNumUnpackStream
	idEmptyStream
	idEmptyFile
	_
	idName
	_
	_
	idMTime
	idWinAttributes
)

var method7zAES = []byte{0x06, 0xF1, 0x07, 0x01}

var (
	methodCopy  = []byte{0x00}
	methodLZMA  = []byte{0x03, 0x01, 0x01}
	methodLZMA2 = []byte{0x21}
)

const idEncodedHeader = 0x17

type writerOptions struct {
	solid            bool
	password         string
	headerEncryption bool
	level            int
	method           string
}

type writerFileHeader struct {
	Name       string
	Modified   time.Time
	Attributes uint32
	IsDir      bool
}

type writerFile struct {
	header           writerFileHeader
	uncompressedSize uint64
	checksum         uint32
	emptyStream      bool
}

type writerFolder struct {
	files            []*writerFile
	method           []byte
	properties       []byte
	aesProperties    []byte
	packSize         uint64
	compressedSize   uint64
	uncompressedSize uint64
	checksum         uint32
}

type archiveWriter struct {
	w       io.WriteSeeker
	options writerOptions
	files   []*writerFile
	folders []*writerFolder
	current *fileWriter
	solid   *streamEncoder
	closed  bool
}

type fileWriter struct {
	owner       *archiveWriter
	file        *writerFile
	stream      *streamEncoder
	checksum    uint32
	size        uint64
	closed      bool
	closeStream bool
}

type streamEncoder struct {
	folder     *writerFolder
	lzma       io.WriteCloser
	encryption *aesWriter
	packed     *countWriter
	compressed *countWriter
	size       uint64
	closed     bool
}

type countWriter struct {
	w io.Writer
	n uint64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += uint64(n)
	return n, err
}

func newWriter(w io.WriteSeeker, options writerOptions) (*archiveWriter, error) {
	if _, err := w.Seek(32, io.SeekStart); err != nil {
		return nil, err
	}
	return &archiveWriter{w: w, options: options}, nil
}

func (w *archiveWriter) addDirectory(header writerFileHeader) error {
	if w.closed {
		return errors.New("7z writer is closed")
	}
	if w.current != nil {
		return errors.New("close the current file before adding a directory")
	}
	header.IsDir = true
	w.files = append(w.files, &writerFile{header: header, emptyStream: true})
	return nil
}

func (w *archiveWriter) create(header writerFileHeader) (io.WriteCloser, error) {
	if w.closed {
		return nil, errors.New("7z writer is closed")
	}
	if header.IsDir {
		return nil, errors.New("use addDirectory for directory entries")
	}
	if w.current != nil {
		if err := w.current.Close(); err != nil {
			return nil, err
		}
	}

	file := &writerFile{header: header}
	w.files = append(w.files, file)
	stream := w.solid
	closeStream := false
	if stream == nil {
		folder := &writerFolder{}
		w.folders = append(w.folders, folder)
		var err error
		stream, err = newStreamEncoder(w.w, folder, w.options.password, w.options.method, w.options.level)
		if err != nil {
			return nil, err
		}
		if w.options.solid {
			w.solid = stream
		} else {
			closeStream = true
		}
	}
	stream.folder.files = append(stream.folder.files, file)
	w.current = &fileWriter{
		owner:       w,
		file:        file,
		stream:      stream,
		closeStream: closeStream,
	}
	return w.current, nil
}

func newStreamEncoder(dst io.Writer, folder *writerFolder, password, method string, level int) (*streamEncoder, error) {
	dictionarySize := dictionaryForLevel(level)
	packed := &countWriter{w: dst}
	compressed := &countWriter{w: packed}
	var encryption *aesWriter
	compressionTarget := io.Writer(compressed)
	if password != "" {
		var err error
		encryption, err = newAESWriter(packed, password)
		if err != nil {
			return nil, err
		}
		compressed.w = encryption
		compressionTarget = compressed
		folder.aesProperties = append([]byte(nil), encryption.properties...)
	}
	if method == "" {
		if level == 0 {
			method = "copy"
		} else {
			method = "lzma2"
		}
	}
	if method == "store" {
		method = "copy"
	}
	var encoder io.WriteCloser
	switch method {
	case "copy":
		encoder = nopWriteCloser{Writer: compressionTarget}
		folder.method = append([]byte(nil), methodCopy...)
	case "lzma":
		properties := make([]byte, 5)
		properties[0] = (lzma.Properties{LC: 3, LP: 0, PB: 2}).Code()
		binary.LittleEndian.PutUint32(properties[1:], uint32(dictionarySize))
		raw := &skipWriter{w: compressionTarget, remaining: lzma.HeaderLen}
		var err error
		encoder, err = (lzma.WriterConfig{DictCap: dictionarySize}).NewWriter(raw)
		if err != nil {
			return nil, err
		}
		folder.method = append([]byte(nil), methodLZMA...)
		folder.properties = properties
	case "lzma2":
		var err error
		if level > 0 && level <= 2 {
			encoder, err = newFastLZMA2Writer(compressionTarget, dictionarySize)
		} else {
			encoder, err = (lzma.Writer2Config{DictCap: dictionarySize}).NewWriter2(compressionTarget)
		}
		if err != nil {
			return nil, err
		}
		folder.method = append([]byte(nil), methodLZMA2...)
		folder.properties = []byte{lzma.EncodeDictCap(int64(dictionarySize))}
	default:
		return nil, fmt.Errorf("unsupported 7z compression method %q", method)
	}
	return &streamEncoder{
		folder:     folder,
		lzma:       encoder,
		encryption: encryption,
		packed:     packed,
		compressed: compressed,
	}, nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type skipWriter struct {
	w         io.Writer
	remaining int
}

func (w *skipWriter) Write(p []byte) (int, error) {
	original := len(p)
	if w.remaining >= len(p) {
		w.remaining -= len(p)
		return original, nil
	}
	skipped := w.remaining
	p = p[skipped:]
	w.remaining = 0
	n, err := w.w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return skipped + n, err
}

func dictionaryForLevel(level int) int {
	switch {
	case level <= 0:
		return 8 << 20
	case level <= 2:
		return 1 << 20
	case level <= 4:
		return 4 << 20
	case level <= 5:
		return 16 << 20
	case level <= 7:
		return 32 << 20
	default:
		return 64 << 20
	}
}

func (w *streamEncoder) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("7z stream encoder is closed")
	}
	n, err := w.lzma.Write(p)
	w.size += uint64(n)
	return n, err
}

func (w *streamEncoder) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.lzma.Close(); err != nil {
		return err
	}
	if w.encryption != nil {
		if err := w.encryption.Close(); err != nil {
			return err
		}
	}
	w.folder.packSize = w.packed.n
	w.folder.compressedSize = w.compressed.n
	w.folder.uncompressedSize = w.size
	return nil
}

func (w *fileWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("7z file writer is closed")
	}
	n, err := w.stream.Write(p)
	w.checksum = crc32.Update(w.checksum, crc32.IEEETable, p[:n])
	w.size += uint64(n)
	return n, err
}

func (w *fileWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.file.uncompressedSize = w.size
	w.file.checksum = w.checksum
	w.stream.folder.checksum = crc32Combine(w.stream.folder.checksum, w.checksum, w.size)
	if w.closeStream {
		if err := w.stream.Close(); err != nil {
			return err
		}
	}
	w.owner.current = nil
	return nil
}

func (w *archiveWriter) Close() error {
	if w.closed {
		return nil
	}
	if w.current != nil {
		if err := w.current.Close(); err != nil {
			return err
		}
	}
	if w.solid != nil {
		if err := w.solid.Close(); err != nil {
			return err
		}
	}
	w.closed = true

	header, err := w.nextHeader()
	if err != nil {
		return err
	}
	if w.options.headerEncryption {
		if w.options.password == "" {
			return errors.New("7z header encryption requires a password")
		}
		packPosition, seekErr := w.w.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return seekErr
		}
		packed := &countWriter{w: w.w}
		encryption, encryptionErr := newAESWriter(packed, w.options.password)
		if encryptionErr != nil {
			return encryptionErr
		}
		if _, encryptionErr = encryption.Write(header); encryptionErr != nil {
			return encryptionErr
		}
		if encryptionErr = encryption.Close(); encryptionErr != nil {
			return encryptionErr
		}
		header = encodedHeader(
			uint64(packPosition-32),
			packed.n,
			uint64(len(header)),
			crc32.ChecksumIEEE(header),
			encryption.properties,
		)
	}
	headerOffset, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(header); err != nil {
		return err
	}

	var start bytes.Buffer
	_ = binary.Write(&start, binary.LittleEndian, uint64(headerOffset-32))
	_ = binary.Write(&start, binary.LittleEndian, uint64(len(header)))
	_ = binary.Write(&start, binary.LittleEndian, crc32.ChecksumIEEE(header))

	var signature bytes.Buffer
	signature.Write([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C})
	signature.WriteByte(0)
	signature.WriteByte(4)
	_ = binary.Write(&signature, binary.LittleEndian, crc32.ChecksumIEEE(start.Bytes()))
	signature.Write(start.Bytes())
	if _, err := w.w.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = w.w.Write(signature.Bytes())
	return err
}

func encodedHeader(packPosition, packSize, unpackSize uint64, checksum uint32, properties []byte) []byte {
	var out bytes.Buffer
	out.WriteByte(idEncodedHeader)
	out.WriteByte(idPackInfo)
	writeUint64(&out, packPosition)
	writeUint64(&out, 1)
	out.WriteByte(idSize)
	writeUint64(&out, packSize)
	out.WriteByte(idEnd)

	out.WriteByte(idUnpackInfo)
	out.WriteByte(idFolder)
	writeUint64(&out, 1)
	out.WriteByte(0)
	writeUint64(&out, 1)
	writeCoder(&out, method7zAES, properties)
	out.WriteByte(idCodersUnpackSize)
	writeUint64(&out, unpackSize)
	out.WriteByte(idCRC)
	out.WriteByte(1)
	_ = binary.Write(&out, binary.LittleEndian, checksum)
	out.WriteByte(idEnd)

	out.WriteByte(idSubStreamsInfo)
	out.WriteByte(idEnd)
	out.WriteByte(idEnd)
	return out.Bytes()
}

func (w *archiveWriter) nextHeader() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte(idHeader)
	if len(w.files) == 0 {
		out.WriteByte(idEnd)
		return out.Bytes(), nil
	}

	if len(w.folders) > 0 {
		out.WriteByte(idMainStreamsInfo)
		writeStreamsInfo(&out, w.folders)
		out.WriteByte(idEnd)
	}

	out.WriteByte(idFilesInfo)
	writeUint64(&out, uint64(len(w.files)))
	writeEmptyStreams(&out, w.files)
	writeNames(&out, w.files)
	writeModifiedTimes(&out, w.files)
	writeAttributes(&out, w.files)
	out.WriteByte(idEnd)
	out.WriteByte(idEnd)
	return out.Bytes(), nil
}

func writeStreamsInfo(out *bytes.Buffer, folders []*writerFolder) {
	out.WriteByte(idPackInfo)
	writeUint64(out, 0)
	writeUint64(out, uint64(len(folders)))
	out.WriteByte(idSize)
	for _, folder := range folders {
		writeUint64(out, folder.packSize)
	}
	out.WriteByte(idEnd)

	out.WriteByte(idUnpackInfo)
	out.WriteByte(idFolder)
	writeUint64(out, uint64(len(folders)))
	out.WriteByte(0)
	for _, folder := range folders {
		writeFolder(out, folder)
	}
	out.WriteByte(idCodersUnpackSize)
	for _, folder := range folders {
		if len(folder.aesProperties) > 0 {
			writeUint64(out, folder.compressedSize)
		}
		writeUint64(out, folder.uncompressedSize)
	}
	out.WriteByte(idCRC)
	out.WriteByte(1)
	for _, folder := range folders {
		_ = binary.Write(out, binary.LittleEndian, folder.checksum)
	}
	out.WriteByte(idEnd)

	out.WriteByte(idSubStreamsInfo)
	multiple := false
	for _, folder := range folders {
		if len(folder.files) != 1 {
			multiple = true
			break
		}
	}
	if multiple {
		out.WriteByte(idNumUnpackStream)
		for _, folder := range folders {
			writeUint64(out, uint64(len(folder.files)))
		}
		out.WriteByte(idSize)
		for _, folder := range folders {
			for i := 0; i+1 < len(folder.files); i++ {
				writeUint64(out, folder.files[i].uncompressedSize)
			}
		}
		out.WriteByte(idCRC)
		out.WriteByte(1)
		for _, folder := range folders {
			if len(folder.files) == 1 {
				continue
			}
			for _, file := range folder.files {
				_ = binary.Write(out, binary.LittleEndian, file.checksum)
			}
		}
	}
	out.WriteByte(idEnd)
}

func writeFolder(out *bytes.Buffer, folder *writerFolder) {
	if len(folder.aesProperties) == 0 {
		writeUint64(out, 1)
		writeCoder(out, folder.method, folder.properties)
		return
	}
	writeUint64(out, 2)
	writeCoder(out, method7zAES, folder.aesProperties)
	writeCoder(out, folder.method, folder.properties)
	writeUint64(out, 1) // Compression input stream
	writeUint64(out, 0) // AES output stream
}

func writeCoder(out *bytes.Buffer, method, properties []byte) {
	flags := byte(len(method))
	if len(properties) > 0 {
		flags |= 0x20
	}
	out.WriteByte(flags)
	out.Write(method)
	if len(properties) > 0 {
		writeUint64(out, uint64(len(properties)))
		out.Write(properties)
	}
}

func writeEmptyStreams(out *bytes.Buffer, files []*writerFile) {
	empty := make([]bool, len(files))
	any := false
	for i, file := range files {
		empty[i] = file.emptyStream
		any = any || file.emptyStream
	}
	if !any {
		return
	}
	out.WriteByte(idEmptyStream)
	writeUint64(out, uint64((len(empty)+7)/8))
	writeBoolVector(out, empty)

	emptyFiles := make([]bool, 0)
	anyEmptyFile := false
	for _, file := range files {
		if file.emptyStream {
			isFile := !file.header.IsDir
			emptyFiles = append(emptyFiles, isFile)
			anyEmptyFile = anyEmptyFile || isFile
		}
	}
	if anyEmptyFile {
		out.WriteByte(idEmptyFile)
		writeUint64(out, uint64((len(emptyFiles)+7)/8))
		writeBoolVector(out, emptyFiles)
	}
}

func writeBoolVector(out *bytes.Buffer, values []bool) {
	var value, mask byte = 0, 0x80
	for _, set := range values {
		if set {
			value |= mask
		}
		mask >>= 1
		if mask == 0 {
			out.WriteByte(value)
			value, mask = 0, 0x80
		}
	}
	if mask != 0x80 {
		out.WriteByte(value)
	}
}

func writeNames(out *bytes.Buffer, files []*writerFile) {
	var property bytes.Buffer
	property.WriteByte(0)
	for _, file := range files {
		for _, value := range utf16.Encode([]rune(file.header.Name)) {
			_ = binary.Write(&property, binary.LittleEndian, value)
		}
		_ = binary.Write(&property, binary.LittleEndian, uint16(0))
	}
	out.WriteByte(idName)
	writeUint64(out, uint64(property.Len()))
	out.Write(property.Bytes())
}

func writeModifiedTimes(out *bytes.Buffer, files []*writerFile) {
	defined := make([]bool, len(files))
	allDefined := true
	anyDefined := false
	for i, file := range files {
		defined[i] = !file.header.Modified.IsZero()
		allDefined = allDefined && defined[i]
		anyDefined = anyDefined || defined[i]
	}
	if !anyDefined {
		return
	}

	var property bytes.Buffer
	if allDefined {
		property.WriteByte(1)
	} else {
		property.WriteByte(0)
		writeBoolVector(&property, defined)
	}
	property.WriteByte(0)
	for i, file := range files {
		if !defined[i] {
			continue
		}
		_ = binary.Write(&property, binary.LittleEndian, filetime(file.header.Modified))
	}
	out.WriteByte(idMTime)
	writeUint64(out, uint64(property.Len()))
	out.Write(property.Bytes())
}

func writeAttributes(out *bytes.Buffer, files []*writerFile) {
	var property bytes.Buffer
	property.WriteByte(1)
	property.WriteByte(0)
	for _, file := range files {
		_ = binary.Write(&property, binary.LittleEndian, file.header.Attributes)
	}
	out.WriteByte(idWinAttributes)
	writeUint64(out, uint64(property.Len()))
	out.Write(property.Bytes())
}

func writeUint64(w io.ByteWriter, value uint64) {
	for extra := 0; extra < 8; extra++ {
		limitBits := 7 + extra*7
		if value < uint64(1)<<limitBits {
			prefix := byte(0xFF << (8 - extra))
			_ = w.WriteByte(prefix | byte(value>>(8*extra)))
			for i := 0; i < extra; i++ {
				_ = w.WriteByte(byte(value >> (8 * i)))
			}
			return
		}
	}
	_ = w.WriteByte(0xFF)
	for i := 0; i < 8; i++ {
		_ = w.WriteByte(byte(value >> (8 * i)))
	}
}

func filetime(value time.Time) uint64 {
	const unixToWindowsSeconds = 11644473600
	return uint64(value.Unix()+unixToWindowsSeconds)*10_000_000 + uint64(value.Nanosecond()/100)
}

type aesWriter struct {
	dst        io.Writer
	mode       cipher.BlockMode
	properties []byte
	buffer     []byte
	closed     bool
}

func newAESWriter(dst io.Writer, password string) (*aesWriter, error) {
	const cycles = 19
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	key := calculate7zKey(password, cycles, nil)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	properties := []byte{cycles | 0x40, aes.BlockSize - 1}
	properties = append(properties, iv...)
	return &aesWriter{
		dst:        dst,
		mode:       cipher.NewCBCEncrypter(block, iv),
		properties: properties,
	}, nil
}

func (w *aesWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("7z AES writer is closed")
	}
	original := len(p)
	w.buffer = append(w.buffer, p...)
	complete := len(w.buffer) / aes.BlockSize * aes.BlockSize
	if complete == 0 {
		return original, nil
	}
	w.mode.CryptBlocks(w.buffer[:complete], w.buffer[:complete])
	n, err := w.dst.Write(w.buffer[:complete])
	if err == nil && n != complete {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	remaining := copy(w.buffer, w.buffer[complete:])
	w.buffer = w.buffer[:remaining]
	return original, nil
}

func (w *aesWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.buffer) == 0 {
		return nil
	}
	block := make([]byte, aes.BlockSize)
	copy(block, w.buffer)
	w.mode.CryptBlocks(block, block)
	_, err := w.dst.Write(block)
	return err
}

func calculate7zKey(password string, cycles uint, salt []byte) []byte {
	encoded := make([]byte, 0, len(password)*2)
	for _, value := range utf16.Encode([]rune(password)) {
		encoded = binary.LittleEndian.AppendUint16(encoded, value)
	}
	input := make([]byte, len(salt)+len(encoded)+8)
	copy(input, salt)
	copy(input[len(salt):], encoded)
	counter := input[len(input)-8:]
	h := sha256.New()
	for i := uint64(0); i < uint64(1)<<cycles; i++ {
		binary.LittleEndian.PutUint64(counter, i)
		_, _ = h.Write(input)
	}
	return h.Sum(nil)
}

func crc32Combine(first, second uint32, secondLength uint64) uint32 {
	if secondLength == 0 {
		return first
	}
	for bit := 0; secondLength != 0; bit++ {
		if secondLength&1 != 0 {
			first = crc32MatrixTimes(&crc32CombineOperators[bit], first)
		}
		secondLength >>= 1
	}
	return first ^ second
}

//nolint:gochecknoglobals
var crc32CombineOperators = func() [64][32]uint32 {
	var operators [64][32]uint32
	var odd, even [32]uint32
	odd[0] = crc32.IEEE
	row := uint32(1)
	for i := 1; i < len(odd); i++ {
		odd[i] = row
		row <<= 1
	}
	crc32MatrixSquare(&even, &odd)
	crc32MatrixSquare(&odd, &even)
	crc32MatrixSquare(&operators[0], &odd)
	for i := 1; i < len(operators); i++ {
		crc32MatrixSquare(&operators[i], &operators[i-1])
	}
	return operators
}()

func crc32MatrixSquare(square, matrix *[32]uint32) {
	for i := range square {
		square[i] = crc32MatrixTimes(matrix, matrix[i])
	}
}

func crc32MatrixTimes(matrix *[32]uint32, vector uint32) uint32 {
	var sum uint32
	for i := 0; vector != 0; i++ {
		if vector&1 != 0 {
			sum ^= matrix[i]
		}
		vector >>= 1
	}
	return sum
}
