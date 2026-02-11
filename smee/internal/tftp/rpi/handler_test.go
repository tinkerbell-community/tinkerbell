package rpi

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
)

// mockReaderFrom implements io.ReaderFrom for testing.
type mockReaderFrom struct {
	buf  *bytes.Buffer
	size int64
}

func (m *mockReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	return m.buf.ReadFrom(r)
}

func (m *mockReaderFrom) SetSize(size int64) {
	m.size = size
}

func TestExtractMAC(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		wantMAC    string
		wantErr    bool
	}{
		{
			name:       "MAC with dashes",
			identifier: "b8-27-eb-12-34-56",
			wantMAC:    "b8:27:eb:12:34:56",
			wantErr:    false,
		},
		{
			name:       "Serial number format",
			identifier: "b827eb123456",
			wantMAC:    "b8:27:eb:12:34:56",
			wantErr:    false,
		},
		{
			name:       "Different MAC prefix with dashes",
			identifier: "dc-a6-32-ab-cd-ef",
			wantMAC:    "dc:a6:32:ab:cd:ef",
			wantErr:    false,
		},
		{
			name:       "Invalid format",
			identifier: "xyz",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mac, err := extractMAC(tt.identifier)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractMAC() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if mac.String() != tt.wantMAC {
					t.Errorf("extractMAC() = %v, want %v", mac.String(), tt.wantMAC)
				}
			}
		})
	}
}

func TestRPIPathPattern(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPass bool
	}{
		{
			name:     "Serial with config.txt",
			path:     "b827eb123456/config.txt",
			wantPass: true,
		},
		{
			name:     "MAC with dashes and cmdline.txt",
			path:     "b8-27-eb-12-34-56/cmdline.txt",
			wantPass: true,
		},
		{
			name:     "Serial with kernel",
			path:     "dca632abcdef/kernel8.img",
			wantPass: true,
		},
		{
			name:     "Nested path",
			path:     "b827eb123456/overlays/vc4-kms-v3d.dtbo",
			wantPass: true,
		},
		{
			name:     "Invalid - no directory",
			path:     "config.txt",
			wantPass: false,
		},
		{
			name:     "Invalid - short serial",
			path:     "b827/config.txt",
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := rpiPathPattern.FindStringSubmatch(tt.path)
			passed := len(matches) >= 3
			if passed != tt.wantPass {
				t.Errorf("pattern match for %s: got %v, want %v (matches: %v)", tt.path, passed, tt.wantPass, matches)
			}
		})
	}
}

func TestServeTemplate(t *testing.T) {
	handler := Handler{
		Logger:   testr.New(t),
		CacheDir: t.TempDir(),
	}

	tests := []struct {
		name         string
		filename     string
		template     string
		mac          net.HardwareAddr
		serial       string
		wantContains []string
	}{
		{
			name:     "config.txt template",
			filename: "config.txt",
			template: ConfigTemplates["config.txt"],
			mac:      net.HardwareAddr{0xb8, 0x27, 0xeb, 0x12, 0x34, 0x56},
			serial:   "b827eb123456",
			wantContains: []string{
				"# Raspberry Pi Config",
				"b8:27:eb:12:34:56",
				"kernel=kernel8.img",
				"enable_uart=1",
			},
		},
		{
			name:     "cmdline.txt template",
			filename: "cmdline.txt",
			template: ConfigTemplates["cmdline.txt"],
			mac:      net.HardwareAddr{0xdc, 0xa6, 0x32, 0xab, 0xcd, 0xef},
			serial:   "dca632abcdef",
			wantContains: []string{
				"console=serial0,115200",
				"console=tty1",
				"root=/dev/mmcblk0p2",
				"tinkerbell=1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := &mockReaderFrom{buf: &bytes.Buffer{}}

			// We can't easily test with real span, so we'll create a no-op span
			// In real usage, this is passed from ServeTFTP
			err := handler.serveTemplate(tt.filename, tt.template, tt.mac, tt.serial, rf, nil, handler.Logger)
			if err != nil {
				t.Fatalf("serveTemplate() error = %v", err)
			}

			content := rf.buf.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(content, want) {
					t.Errorf("serveTemplate() content missing %q\nGot:\n%s", want, content)
				}
			}

			if rf.size <= 0 {
				t.Errorf("SetSize() was not called or size is invalid: %d", rf.size)
			}
		})
	}
}

func TestServeRawFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	testContent := []byte("This is a test kernel file")
	testFile := filepath.Join(tmpDir, "b827eb123456", "kernel8.img")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatal(err)
	}

	handler := Handler{
		Logger:   testr.New(t),
		CacheDir: tmpDir,
	}

	tests := []struct {
		name     string
		filename string
		wantErr  bool
		wantData []byte
	}{
		{
			name:     "existing file",
			filename: "b827eb123456/kernel8.img",
			wantErr:  false,
			wantData: testContent,
		},
		{
			name:     "non-existent file",
			filename: "b827eb123456/notfound.img",
			wantErr:  true,
		},
		{
			name:     "path traversal attempt",
			filename: "../../../etc/passwd",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := &mockReaderFrom{buf: &bytes.Buffer{}}

			err := handler.serveRawFile(tt.filename, rf, nil, handler.Logger)
			if (err != nil) != tt.wantErr {
				t.Errorf("serveRawFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if !bytes.Equal(rf.buf.Bytes(), tt.wantData) {
					t.Errorf("serveRawFile() got %q, want %q", rf.buf.Bytes(), tt.wantData)
				}
				if rf.size != int64(len(tt.wantData)) {
					t.Errorf("SetSize() got %d, want %d", rf.size, len(tt.wantData))
				}
			}
		})
	}
}

func TestServeTFTP_Integration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test raw file
	rawContent := []byte("raw kernel content")
	rawFile := filepath.Join(tmpDir, "b827eb123456", "start4.elf")
	if err := os.MkdirAll(filepath.Dir(rawFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rawFile, rawContent, 0644); err != nil {
		t.Fatal(err)
	}

	handler := Handler{
		Logger:   testr.New(t),
		CacheDir: tmpDir,
	}

	tests := []struct {
		name         string
		filename     string
		wantErr      bool
		wantContains string
		isRaw        bool
	}{
		{
			name:         "template config.txt",
			filename:     "b827eb123456/config.txt",
			wantErr:      false,
			wantContains: "# Raspberry Pi Config",
			isRaw:        false,
		},
		{
			name:         "template cmdline.txt",
			filename:     "b8-27-eb-12-34-56/cmdline.txt",
			wantErr:      false,
			wantContains: "console=serial0,115200",
			isRaw:        false,
		},
		{
			name:         "raw file",
			filename:     "b827eb123456/start4.elf",
			wantErr:      false,
			wantContains: "raw kernel content",
			isRaw:        true,
		},
		{
			name:     "invalid path format",
			filename: "config.txt",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := &mockReaderFrom{buf: &bytes.Buffer{}}

			err := handler.ServeTFTP(tt.filename, rf)
			if (err != nil) != tt.wantErr {
				t.Errorf("ServeTFTP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.wantContains != "" {
				content := rf.buf.String()
				if !strings.Contains(content, tt.wantContains) {
					t.Errorf("ServeTFTP() content missing %q\nGot:\n%s", tt.wantContains, content)
				}
			}
		})
	}
}
