package noop

import (
	"context"
	"errors"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

var errAlways = errors.New("noop backend always returns an error")

type Backend struct{}

func (n Backend) FilterHardwareV1Alpha2(_ context.Context, _ data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return nil, errAlways
}
