package dhcp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

const (
	// U-Boot image header magic number
	IH_MAGIC = 0x27051956

	// U-Boot image types
	IH_TYPE_INVALID    = 0
	IH_TYPE_STANDALONE = 1
	IH_TYPE_KERNEL     = 2
	IH_TYPE_RAMDISK    = 3
	IH_TYPE_MULTI      = 4
	IH_TYPE_FIRMWARE   = 5
	IH_TYPE_SCRIPT     = 6
	IH_TYPE_FILESYSTEM = 7
	IH_TYPE_FLATDT     = 8

	// U-Boot compression types
	IH_COMP_NONE  = 0
	IH_COMP_GZIP  = 1
	IH_COMP_BZIP2 = 2
	IH_COMP_LZMA  = 3
	IH_COMP_LZO   = 4

	// U-Boot CPU architectures
	IH_ARCH_INVALID    = 0
	IH_ARCH_ALPHA      = 1
	IH_ARCH_ARM        = 2
	IH_ARCH_I386       = 3
	IH_ARCH_IA64       = 4
	IH_ARCH_MIPS       = 5
	IH_ARCH_MIPS64     = 6
	IH_ARCH_PPC        = 7
	IH_ARCH_S390       = 8
	IH_ARCH_SH         = 9
	IH_ARCH_SPARC      = 10
	IH_ARCH_SPARC64    = 11
	IH_ARCH_M68K       = 12
	IH_ARCH_NIOS       = 13
	IH_ARCH_MICROBLAZE = 14
	IH_ARCH_NIOS2      = 15
	IH_ARCH_BLACKFIN   = 16
	IH_ARCH_AVR32      = 17
	IH_ARCH_ST200      = 18
	IH_ARCH_SANDBOX    = 19
	IH_ARCH_NDS32      = 20
	IH_ARCH_OPENRISC   = 21
	IH_ARCH_ARM64      = 22
	IH_ARCH_ARC        = 23
	IH_ARCH_X86_64     = 24
	IH_ARCH_XTENSA     = 25
	IH_ARCH_RISCV      = 26

	// U-Boot operating systems
	IH_OS_INVALID   = 0
	IH_OS_OPENBSD   = 1
	IH_OS_NETBSD    = 2
	IH_OS_FREEBSD   = 3
	IH_OS_4_4BSD    = 4
	IH_OS_LINUX     = 5
	IH_OS_SVR4      = 6
	IH_OS_ESIX      = 7
	IH_OS_SOLARIS   = 8
	IH_OS_IRIX      = 9
	IH_OS_SCO       = 10
	IH_OS_DELL      = 11
	IH_OS_NCR       = 12
	IH_OS_LYNXOS    = 13
	IH_OS_VXWORKS   = 14
	IH_OS_PSOS      = 15
	IH_OS_QNX       = 16
	IH_OS_U_BOOT    = 17
	IH_OS_RTEMS     = 18
	IH_OS_ARTOS     = 19
	IH_OS_UNITY     = 20
	IH_OS_INTEGRITY = 21
	IH_OS_OSE       = 22
	IH_OS_PLAN9     = 23
	IH_OS_OPENRTOS  = 24
)

// UBootImageHeader represents the U-Boot legacy image format header (64 bytes).
// This matches the structure defined in U-Boot's include/image.h.
type UBootImageHeader struct {
	Magic      uint32   // Image Header Magic Number (0x27051956)
	HeaderCRC  uint32   // Image Header CRC Checksum
	Time       uint32   // Image Creation Timestamp
	Size       uint32   // Image Data Size
	LoadAddr   uint32   // Data Load Address
	EntryPoint uint32   // Entry Point Address
	DataCRC    uint32   // Image Data CRC Checksum
	OS         uint8    // Operating System
	Arch       uint8    // CPU architecture
	Type       uint8    // Image Type
	Comp       uint8    // Compression Type
	Name       [32]byte // Image Name (null-terminated)
}

// MkImageOptions holds configuration for creating U-Boot images.
type MkImageOptions struct {
	// Architecture (e.g., IH_ARCH_ARM64)
	Arch uint8
	// Image type (e.g., IH_TYPE_SCRIPT)
	Type uint8
	// Compression type (e.g., IH_COMP_NONE)
	Compression uint8
	// Image name (max 31 characters + null terminator)
	Name string
	// Operating system (default: IH_OS_LINUX)
	OS uint8
	// Load address (usually 0 for scripts)
	LoadAddr uint32
	// Entry point (usually 0 for scripts)
	EntryPoint uint32
}

// MkImage creates a U-Boot legacy image from the input data.
// This is equivalent to: mkimage -A <arch> -T <type> -C <comp> -n <name> -d input output
func MkImage(data []byte, opts MkImageOptions) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("input data is empty")
	}

	// Default to Linux if OS not specified
	if opts.OS == 0 {
		opts.OS = IH_OS_LINUX
	}

	// Validate and truncate name if necessary
	name := opts.Name
	if len(name) > 31 {
		name = name[:31]
	}

	// Calculate data CRC32 (U-Boot uses big-endian CRC32)
	dataCRC := crc32.ChecksumIEEE(data)

	// Create header
	header := UBootImageHeader{
		Magic:      IH_MAGIC,
		HeaderCRC:  0, // Will be calculated after filling other fields
		Time:       uint32(time.Now().Unix()),
		Size:       uint32(len(data)),
		LoadAddr:   opts.LoadAddr,
		EntryPoint: opts.EntryPoint,
		DataCRC:    dataCRC,
		OS:         opts.OS,
		Arch:       opts.Arch,
		Type:       opts.Type,
		Comp:       opts.Compression,
	}

	// Copy name into header (null-terminated)
	copy(header.Name[:], name)

	// Calculate header CRC (with HeaderCRC field set to 0)
	headerBuf := new(bytes.Buffer)
	if err := binary.Write(headerBuf, binary.BigEndian, &header); err != nil {
		return nil, fmt.Errorf("failed to serialize header: %w", err)
	}
	headerBytes := headerBuf.Bytes()

	// Calculate CRC32 of header (excluding the CRC field itself)
	// Set CRC field to 0 for calculation
	binary.BigEndian.PutUint32(headerBytes[4:8], 0)
	headerCRC := crc32.ChecksumIEEE(headerBytes)

	// Update header with calculated CRC
	header.HeaderCRC = headerCRC

	// Write final image (header + data)
	result := new(bytes.Buffer)
	if err := binary.Write(result, binary.BigEndian, &header); err != nil {
		return nil, fmt.Errorf("failed to write header: %w", err)
	}
	if _, err := result.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write data: %w", err)
	}

	return result.Bytes(), nil
}

// MkImageScript creates a U-Boot script image from script text.
// This is equivalent to: mkimage -A arm64 -T script -C none -n "name" -d script.cmd script.scr
func MkImageScript(scriptText string, arch uint8, name string) ([]byte, error) {
	opts := MkImageOptions{
		Arch:        arch,
		Type:        IH_TYPE_SCRIPT,
		Compression: IH_COMP_NONE,
		Name:        name,
		OS:          IH_OS_LINUX,
		LoadAddr:    0,
		EntryPoint:  0,
	}

	return MkImage([]byte(scriptText), opts)
}

// MkImageScriptARM64 creates an ARM64 U-Boot script image.
// This is the most common use case for Raspberry Pi 4 and similar ARM64 devices.
func MkImageScriptARM64(scriptText string, name string) ([]byte, error) {
	return MkImageScript(scriptText, IH_ARCH_ARM64, name)
}

// ParseUBootImage parses a U-Boot image and returns the header and data.
func ParseUBootImage(image []byte) (*UBootImageHeader, []byte, error) {
	if len(image) < 64 {
		return nil, nil, fmt.Errorf("image too small: must be at least 64 bytes")
	}

	// Parse header
	header := &UBootImageHeader{}
	headerReader := bytes.NewReader(image[:64])
	if err := binary.Read(headerReader, binary.BigEndian, header); err != nil {
		return nil, nil, fmt.Errorf("failed to parse header: %w", err)
	}

	// Verify magic number
	if header.Magic != IH_MAGIC {
		return nil, nil, fmt.Errorf("invalid magic number: expected 0x%08X, got 0x%08X",
			IH_MAGIC, header.Magic)
	}

	// Verify header CRC
	savedCRC := header.HeaderCRC
	header.HeaderCRC = 0
	headerBuf := new(bytes.Buffer)
	if err := binary.Write(headerBuf, binary.BigEndian, header); err != nil {
		return nil, nil, fmt.Errorf("failed to serialize header for CRC check: %w", err)
	}
	calculatedCRC := crc32.ChecksumIEEE(headerBuf.Bytes())
	if savedCRC != calculatedCRC {
		return nil, nil, fmt.Errorf("header CRC mismatch: expected 0x%08X, got 0x%08X",
			calculatedCRC, savedCRC)
	}
	header.HeaderCRC = savedCRC

	// Extract data
	if len(image) < 64+int(header.Size) {
		return nil, nil, fmt.Errorf("image truncated: expected %d bytes, got %d",
			64+header.Size, len(image))
	}
	data := image[64 : 64+header.Size]

	// Verify data CRC
	dataCRC := crc32.ChecksumIEEE(data)
	if dataCRC != header.DataCRC {
		return nil, nil, fmt.Errorf("data CRC mismatch: expected 0x%08X, got 0x%08X",
			header.DataCRC, dataCRC)
	}

	return header, data, nil
}

// WriteUBootImageToWriter writes a U-Boot image to an io.Writer.
// This is useful for streaming images directly to HTTP responses or files.
func WriteUBootImageToWriter(w io.Writer, data []byte, opts MkImageOptions) error {
	image, err := MkImage(data, opts)
	if err != nil {
		return err
	}

	_, err = w.Write(image)
	return err
}
