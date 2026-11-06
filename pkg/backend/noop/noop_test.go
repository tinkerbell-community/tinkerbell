package noop

import (
	"context"
	"errors"
	"testing"

	"github.com/tinkerbell/tinkerbell/pkg/data"
)

func TestBackend(t *testing.T) {
	b := Backend{}
	ctx := context.Background()
	_, err := b.FilterHardware(ctx, data.HardwareFilter{})
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, errAlways) {
		t.Error("expected errAlways")
	}
	if _, err := b.FilterHardwareV1Alpha2(ctx, data.HardwareFilter{}); !errors.Is(err, errAlways) {
		t.Errorf("FilterHardwareV1Alpha2() error = %v, want errAlways", err)
	}
}
