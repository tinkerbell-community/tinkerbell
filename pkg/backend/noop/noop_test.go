package noop

import (
	"context"
	"errors"
	"testing"

	"github.com/tinkerbell/tinkerbell/pkg/data"
)

func TestBackend(t *testing.T) {
	b := Backend{}
	if _, err := b.FilterHardwareV1Alpha2(context.Background(), data.HardwareFilter{}); !errors.Is(err, errAlways) {
		t.Errorf("FilterHardwareV1Alpha2() error = %v, want errAlways", err)
	}
}
