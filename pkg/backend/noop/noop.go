package noop

import (
	"context"
	"errors"

	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

var errAlways = errors.New("noop backend always returns an error")

type Backend struct{}

func (n Backend) ReadHardware(_ context.Context, _, _ string) (*tinkerbell.Hardware, error) {
	return nil, errAlways
}

func (n Backend) FilterHardware(_ context.Context, _ data.HardwareFilter) (*tinkerbell.Hardware, error) {
	return nil, errAlways
}

func (n Backend) FilterHardwareV1Alpha2(_ context.Context, _ data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return nil, errAlways
}

func (n Backend) UpdateHardware(_ context.Context, _ *tinkerbell.Hardware, _ data.UpdateOptions) error {
	return errAlways
}
