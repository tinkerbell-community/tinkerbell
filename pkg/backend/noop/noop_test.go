package noop

import (
	"context"
	"errors"
	"testing"

	"github.com/tinkerbell/tinkerbell/pkg/data"
)

func TestBackend(t *testing.T) {
	b := Backend{}
	if _, err := b.FilterHardware(context.Background(), data.HardwareFilter{}); !errors.Is(err, errAlways) {
		t.Errorf("FilterHardware() error = %v, want errAlways", err)
	}
}
