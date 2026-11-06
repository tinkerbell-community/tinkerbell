package bmc

const (
	PowerActionOn      PowerAction = "on"
	PowerActionHardOff PowerAction = "off"
	PowerActionSoftOff PowerAction = "soft"
	PowerActionCycle   PowerAction = "cycle"
	PowerActionReset   PowerAction = "reset"
	PowerActionStatus  PowerAction = "status"

	BootDevicePXE   BootDevice = "pxe"
	BootDeviceDisk  BootDevice = "disk"
	BootDeviceBIOS  BootDevice = "bios"
	BootDeviceCDROM BootDevice = "cdrom"
	BootDeviceSafe  BootDevice = "safe"

	// VirtualMediaCD represents a virtual CD-ROM.
	VirtualMediaCD VirtualMediaKind = "CD"
)

// BootDevice represents boot device of the Machine.
type BootDevice string

// VirtualMediaKind represents the kind of virtual media.
type VirtualMediaKind string

// PowerAction represents the power control operation on the baseboard management.
// +kubebuilder:validation:Enum=on;off;soft;status;cycle;reset
type PowerAction string

// VirtualMediaAction represents a virtual media action.
type VirtualMediaAction struct {
	// mediaURL represents the URL of the image to be inserted into the virtual media, or empty to eject media.
	MediaURL string `json:"mediaURL,omitempty"`

	// Kind represents the kind of virtual media.
	Kind VirtualMediaKind `json:"kind"`
}

// BootDeviceConfig represents the configuration for setting a boot device.
// By default, the boot device is set for the next boot only. If Persistent is set to true, the boot device is set persistently as the first boot device.
type BootDeviceConfig struct {
	// Name is the name of the device to set as the first boot device.
	Name BootDevice `json:"name,omitempty"`

	// Persistent indicates whether the boot device should be set persistently as the first boot device.
	Persistent bool `json:"persistent,omitempty"`

	// EFIBoot indicates whether the boot device should be set to efiboot mode.
	EFIBoot bool `json:"efiBoot,omitempty"`
}

// NetworkBootConfig represents the configuration for enabling/disabling network boot protocol
// capabilities (UEFI HTTP Boot, legacy PXE) in BIOS/UEFI firmware, and for pointing UEFI HTTP
// Boot at a boot image URL. Unlike BootDevice (which selects among existing boot options), this
// creates or removes the boot options themselves. HTTPBootEnabled, PXEBootEnabled, HTTPBootURL,
// and HTTPBootTLSMode are independent — any combination may be set at once; a nil field leaves
// that setting untouched.
// +kubebuilder:validation:MinProperties:=1
type NetworkBootConfig struct {
	// HTTPBootEnabled enables (true) or disables (false) UEFI HTTP Boot capability, IPv4 and
	// IPv6 both.
	HTTPBootEnabled *bool `json:"httpBootEnabled,omitempty"`

	// PXEBootEnabled enables (true) or disables (false) legacy PXE boot capability.
	PXEBootEnabled *bool `json:"pxeBootEnabled,omitempty"`

	// HTTPBootURL sets the URL UEFI HTTP Boot fetches its boot image from, via the standard
	// Redfish ComputerSystem.Boot.HttpBootUri property. Independent of HTTPBootEnabled: setting
	// the URL does not enable the capability, and enabling the capability does not require a URL.
	// +kubebuilder:validation:Format=uri
	// +kubebuilder:validation:XValidation:rule="self.startsWith('http://') || self.startsWith('https://')",message="httpBootURL must be an http or https URL"
	HTTPBootURL *string `json:"httpBootURL,omitempty"`

	// HTTPBootTLSMode sets the TLS authentication mode UEFI HTTP Boot uses to connect to the HTTP
	// boot server: "None" allows fetching the boot image over plain HTTP, "OneWay" requires HTTPS
	// with the server authenticated by the client. Some hardware defaults to "OneWay", which
	// rejects an HTTPBootURL that isn't HTTPS. Independent of HTTPBootEnabled and HTTPBootURL.
	// +kubebuilder:validation:Enum=None;OneWay
	HTTPBootTLSMode *string `json:"httpBootTLSMode,omitempty"`
}

func (b BootDevice) String() string {
	return string(b)
}

func (v VirtualMediaKind) String() string {
	return string(v)
}

func (p PowerAction) String() string {
	return string(p)
}

// Operations defines the operations that can be performed against a BMC.
// Only a single field in the struct should be defined at any given time
// as only one operation can be performed at a time.
// For example either PowerAction or BootDevice.
// +kubebuilder:validation:XValidation:rule="(has(self.powerAction) ? 1 : 0) + (has(self.bootDevice) ? 1 : 0) + (has(self.virtualMediaAction) ? 1 : 0) + (has(self.networkBootConfig) ? 1 : 0) == 1",message="only one of powerAction, bootDevice, virtualMediaAction, or networkBootConfig can be specified"
type Operations struct {
	// PowerAction represents a baseboard management power operation.
	// +kubebuilder:validation:Enum=on;off;soft;status;cycle;reset
	PowerAction *PowerAction `json:"powerAction,omitempty"`

	// BootDevice is the device to set as the first boot device on the Machine.
	BootDevice *BootDeviceConfig `json:"bootDevice,omitempty"`

	// VirtualMediaAction represents a baseboard management virtual media insert/eject.
	VirtualMediaAction *VirtualMediaAction `json:"virtualMediaAction,omitempty"`

	// NetworkBootConfig enables/disables network boot protocol capabilities (UEFI HTTP Boot,
	// legacy PXE) in BIOS/UEFI firmware, and/or sets the URL UEFI HTTP Boot fetches its boot
	// image from. Support for the specific BMC vendor is implemented in bmclib.
	NetworkBootConfig *NetworkBootConfig `json:"networkBootConfig,omitempty"`

	// TimeoutSeconds defines the maximum time in seconds the Operation is allowed to run.
	// If not defined, the default from the runtime is used.
	TimeoutSeconds int64 `json:"timeoutSeconds,omitempty"`
}
