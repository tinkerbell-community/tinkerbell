// Package hardware provides hardware lookup against a tinkerbell backend.
// It exposes a BackendReader interface that callers implement, and helpers
// (GetByMac, GetByIP) that return a unified Info struct used by the iPXE
// script and TFTP serving paths.
package hardware

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/smee/internal/dhcp"
)

// BackendReader is the interface for getting data from a backend.
type BackendReader interface {
	FilterHardwareV1Alpha2(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error)
}

// Info holds the unified hardware data used by iPXE script and TFTP serving paths.
type Info struct {
	AllowNetboot  bool // If true, the client will be provided netboot options in the DHCP offer/ack.
	Console       string
	MACAddress    net.HardwareAddr
	Arch          string
	VLANID        string
	AgentID       string
	Facility      string
	IPXEScript    string
	IPXEScriptURL *url.URL
	OSIE          OSIE
	PXELINUX      PXELINUX
	RPI           RPI
}

// OSIE or OS Installation Environment is the data about where the OSIE parts are located.
//
// The kernel and initrd are located by KernelURL and InitrdURL, the Hardware's
// instance.osie, or, for Hardware created through v1alpha1, by BaseURL, Kernel
// and Initrd as v1alpha1 held them. Only one of the two forms is set.
type OSIE struct {
	// KernelURL is the URL of the kernel: absolute, or a reference relative to
	// the default OSIE URL. ${arch} in it is the client's architecture.
	KernelURL string
	// InitrdURL is KernelURL for the initrd.
	InitrdURL string
	// BaseURL is the URL where the OSIE parts are located.
	BaseURL *url.URL
	// Kernel is the name of the kernel file.
	Kernel string
	// Initrd is the name of the initrd file.
	Initrd string
	// KernelParams are the kernel command-line parameters; rendered as cmdline.txt for RPi netboot.
	KernelParams []string
}

// PXELINUX represents the config used to generate pxelinux.cfg for u-boot booting.
type PXELINUX struct {
	Config string `json:"config,omitempty"`
}

// RPI represents the data needed to support RaspberryPi EEPROM firmware netbooting.
type RPI struct {
	SerialNum    string `json:"serialNum"`
	FirmwarePath string `json:"firmwarePath,omitempty"`
	ConfigTxt    string `json:"configTxt,omitempty"`
}

// GetByMac uses the BackendReader to get the hardware data by MAC address and
// translates it to the Info struct.
func GetByMac(ctx context.Context, mac net.HardwareAddr, br BackendReader) (Info, error) {
	if br == nil {
		return Info{}, errors.New("backend is nil")
	}
	spec, err := br.FilterHardwareV1Alpha2(ctx, data.HardwareFilter{ByMACAddress: mac.String()})
	if err != nil {
		return Info{}, err
	}
	hw, err := dhcp.ConvertByMac(ctx, mac, spec)
	if err != nil {
		return Info{}, fmt.Errorf("failed to convert hardware data: %w", err)
	}

	return info(hw)
}

// GetByIP uses the BackendReader to get the hardware data by IP address and
// translates it to the Info struct.
func GetByIP(ctx context.Context, ip net.IP, br BackendReader) (Info, error) {
	if br == nil {
		return Info{}, errors.New("backend is nil")
	}
	spec, err := br.FilterHardwareV1Alpha2(ctx, data.HardwareFilter{ByIPAddress: ip.String()})
	if err != nil {
		return Info{}, err
	}
	hw, err := dhcp.ConvertByIP(ctx, ip, spec)
	if err != nil {
		return Info{}, fmt.Errorf("failed to convert hardware data: %w", err)
	}

	return info(hw)
}

func info(hw dhcp.Hardware) (Info, error) {
	if hw.DHCP == nil {
		return Info{}, errors.New("no dhcp data")
	}
	if hw.Netboot == nil {
		return Info{}, errors.New("no netboot data")
	}
	d := hw.DHCP
	n := hw.Netboot

	return Info{
		AllowNetboot:  n.AllowNetboot,
		Console:       "",
		MACAddress:    d.MACAddress,
		Arch:          d.Arch,
		VLANID:        hw.VLANID(),
		AgentID:       hw.AgentID,
		Facility:      n.Facility,
		IPXEScript:    n.IPXEScript,
		IPXEScriptURL: n.IPXEScriptURL,
		OSIE:          OSIE(n.OSIE),
		PXELINUX:      PXELINUX(n.PXELINUX),
		RPI:           RPI(n.RPI),
	}, nil
}
