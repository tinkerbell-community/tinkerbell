// Package noop is a backend handler that does nothing.
package reservation

import (
	"context"
	"errors"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

// Handler is a noop backend.
type noop struct{}

// FilterHardwareV1Alpha2 returns an error.
func (h noop) FilterHardwareV1Alpha2(_ context.Context, _ data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return nil, errors.New("no backend specified, please specify a backend")
}
