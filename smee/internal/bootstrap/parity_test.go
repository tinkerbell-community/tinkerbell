package bootstrap

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
)

// v1alpha1Files and v1alpha1NetbootAllowed are Files and netbootAllowed as they
// were when Smee read v1alpha1 Hardware.
func v1alpha1Files(hw *v1alpha1.Hardware) ([]File, error) {
	if hw == nil || hw.Spec.Metadata == nil {
		return nil, nil
	}
	files := make([]File, 0, len(hw.Spec.Metadata.OSIEFiles))
	for i, f := range hw.Spec.Metadata.OSIEFiles {
		p := strings.TrimSpace(strings.TrimPrefix(f.Path, "/"))
		if p == "" {
			return nil, fmt.Errorf("spec.metadata.osieFiles[%d] has an empty path", i)
		}
		mode, err := parseMode(f.Mode)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Name: Dir + "/" + p, Mode: mode, Data: []byte(f.Contents)})
	}
	return files, nil
}

func v1alpha1NetbootAllowed(hw *v1alpha1.Hardware, mac net.HardwareAddr) bool {
	for _, i := range hw.Spec.Interfaces {
		if i.DHCP != nil && i.DHCP.MAC == mac.String() {
			return i.Netboot != nil && i.Netboot.AllowPXE != nil && *i.Netboot.AllowPXE
		}
	}
	return false
}

// TestArchiveParity builds the bootstrap archive of every v1alpha1 fixture from
// its v1alpha1 form, the way Smee did before, and from its v1alpha2 form, and
// compares them, and whether each interface may fetch it.
func TestArchiveParity(t *testing.T) {
	for _, f := range paritytest.Fixtures() {
		t.Run(f.Name, func(t *testing.T) {
			v2 := paritytest.V1Alpha2(t, f.Hardware)
			oldFiles, err := v1alpha1Files(f.Hardware)
			if err != nil {
				t.Fatal(err)
			}
			want, err := Archive(oldFiles, f.Hardware.CreationTimestamp.Time)
			if err != nil {
				t.Fatal(err)
			}
			files, err := Files(v2)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Archive(files, ModTime(v2))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("archive differs: %d bytes, want %d", len(got), len(want))
			}
			for _, mac := range f.MACs() {
				if got, want := netbootAllowed(v2, mac), v1alpha1NetbootAllowed(f.Hardware, mac); got != want {
					t.Errorf("%v: netboot allowed = %v, want %v", mac, got, want)
				}
			}
		})
	}
}
