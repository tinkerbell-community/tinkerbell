package iso

import (
	"context"
	"testing"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
)

type v2Backend struct{ hw *v1alpha2.Hardware }

func (b v2Backend) FilterHardwareV1Alpha2(context.Context, data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return b.hw, nil
}

// TestPatchParity builds the ISO kernel argument patch and picks the source ISO
// for every v1alpha1 fixture from the data Smee read from its v1alpha1 form
// before and from its v1alpha2 form now, and compares them.
func TestPatchParity(t *testing.T) {
	h := &Handler{Patch: Patch{
		KernelParams: KernelParams{
			ExtraParams:        []string{"extra=1"},
			Syslog:             "192.168.2.2",
			TinkServerGRPCAddr: "192.168.2.2:42113",
		},
		SourceISO:         "http://default.example.com/hook.iso",
		StaticIPAMEnabled: true,
	}}
	for _, f := range paritytest.Fixtures() {
		backend := v2Backend{hw: paritytest.V1Alpha2(t, f.Hardware)}
		for _, mac := range f.MACs() {
			t.Run(f.Name+"/"+mac.String(), func(t *testing.T) {
				old, err := paritytest.ConvertByMac(mac, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				fac, hw, spec, err := h.getFacility(context.Background(), mac, backend)
				if err != nil {
					t.Fatal(err)
				}
				if spec == nil {
					t.Fatal("no Hardware for the bootstrap slot")
				}
				want := h.constructPatch(facilityConsoles(old.Netboot.Facility), mac.String(), old.DHCP)
				if got := h.constructPatch(facilityConsoles(fac), mac.String(), hw.DHCP); got != want {
					t.Errorf("patch differs:\nv1alpha1: %s\nv1alpha2: %s", want, got)
				}
				wantISO, gotISO := "", ""
				if old.Isoboot != nil {
					wantISO = old.Isoboot.SourceISO.String()
				}
				if hw.Isoboot != nil {
					gotISO = hw.Isoboot.SourceISO.String()
				}
				if gotISO != wantISO {
					t.Errorf("source ISO = %q, want %q", gotISO, wantISO)
				}
			})
		}
	}
}
