//go:build !linux

package network

import (
	"context"
	"fmt"
	"runtime"

	"github.com/go-logr/logr"
)

// EBPFManager is a stub for non-Linux platforms. eBPF DHCP redirection
// requires Linux TC (traffic control) and the cilium/ebpf loader.
type EBPFManager struct{}

func newEBPFManager(_ logr.Logger) (*EBPFManager, error) { //nolint:unusedfunc
	return nil, fmt.Errorf("ebpf interface type requires linux (current: %s)", runtime.GOOS)
}

func (m *EBPFManager) Setup(_ context.Context) error { return nil }
func (m *EBPFManager) Cleanup() error                { return nil }
func (m *EBPFManager) Close() error                  { return nil }
