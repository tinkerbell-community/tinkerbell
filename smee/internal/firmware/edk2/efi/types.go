package efi

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// Ip6ConfigData represents IPv6 configuration data stored in MAC-named variables.
type Ip6ConfigData struct {
	InterfaceId     []byte
	PolicyTable     []Ip6PolicyEntry
	DadTransmits    uint32
	InterfaceInfo   Ip6InterfaceInfo
	Manual          Ip6ManualConfig
	Gateway         []net.IP
	Dns             []net.IP
	MtuSize         uint32
	AcceptRouterAdv bool
}

// Ip6PolicyEntry represents an IPv6 policy table entry.
type Ip6PolicyEntry struct {
	Address    net.IP
	PrefixLen  uint8
	Precedence uint8
	Label      uint8
}

// Ip6InterfaceInfo represents IPv6 interface information.
type Ip6InterfaceInfo struct {
	Name         string
	IfType       uint8
	HwAddressLen uint32
	HwAddress    net.HardwareAddr
	AddressInfo  []Ip6AddressInfo
	RouteTable   []Ip6RouteInfo
}

// Ip6AddressInfo represents IPv6 address configuration.
type Ip6AddressInfo struct {
	Address       net.IP
	PrefixLength  uint8
	AddressOrigin uint8
}

// Ip6RouteInfo represents IPv6 routing information.
type Ip6RouteInfo struct {
	Destination  net.IP
	PrefixLength uint8
	Gateway      net.IP
	Metric       uint32
}

// Ip6ManualConfig represents manual IPv6 configuration.
type Ip6ManualConfig struct {
	Addresses []Ip6AddressInfo
	Routes    []Ip6RouteInfo
}

// NetworkDeviceList represents the _NDL (Network Device List) variable.
type NetworkDeviceList struct {
	Version uint32
	Entries []NetworkDeviceEntry
}

// NetworkDeviceEntry represents a single network device entry.
type NetworkDeviceEntry struct {
	DevicePath    DevicePath
	MacAddress    net.HardwareAddr
	InterfaceType uint32
	Status        uint32
}

// PlatformConfig represents platform-specific configuration variables.
type PlatformConfig struct {
	CpuClock                  uint32
	CustomCpuClock            uint32
	RamMoreThan3GB            bool
	RamLimitTo3GB             bool
	SystemTableMode           uint32
	FanOnGpio                 bool
	FanTemp                   uint32
	XhciPci                   bool
	XhciReload                bool
	SdIsArasan                bool
	MmcDisableMulti           bool
	MmcForce1Bit              bool
	MmcForceDefaultSpeed      bool
	MmcSdDefaultSpeedMHz      uint32
	MmcSdHighSpeedMHz         uint32
	MmcEnableDma              bool
	DebugEnableJTAG           bool
	DisplayEnableScaledVModes uint8
	DisplayEnableSShot        bool
}

// ConsoleConfig represents console-related configuration.
type ConsoleConfig struct {
	ConsolePref uint32
	ConInPath   DevicePath
	ConOutPath  DevicePath
	ErrOutPath  DevicePath
}

// SecurityConfig represents security-related variables.
type SecurityConfig struct {
	CustomMode   bool
	VendorKeysNv bool
	SetupMode    bool
	AuditMode    bool
	DeployedMode bool
}

// TimeConfig represents time-related configuration.
type TimeConfig struct {
	RtcEpochSeconds uint64
	RtcTimeZone     int16
	RtcDaylight     uint8
}

// iSCSIConfig represents iSCSI configuration.
type ISCSIConfig struct {
	AttemptOrder []uint8
	Attempts     []ISCSIAttempt
}

// ISCSIAttempt represents a single iSCSI attempt configuration.
type ISCSIAttempt struct {
	AttemptNumber      uint32
	Name               string
	NicPath            DevicePath
	TargetName         string
	TargetIP           net.IP
	TargetPort         uint16
	BootLun            uint64
	AuthenticationType uint8
	Username           string
	Password           string
	IsId               []byte
	Enabled            bool
}

// KeyData represents key binding data.
type KeyData struct {
	KeyCode    uint32
	ScanCode   uint16
	ShiftState uint32
}

// AssetTag represents asset tag information.
type AssetTag struct {
	Tag string
}

// CertDatabase represents certificate database entries.
type CertDatabase struct {
	Version      uint32
	Certificates []CertEntry
}

// CertEntry represents a single certificate entry.
type CertEntry struct {
	SignatureType GUID
	CertData      []byte
}

// NewIp6ConfigData creates a new Ip6ConfigData from raw bytes.
func NewIp6ConfigData(data []byte) (*Ip6ConfigData, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("IP6 config data too short")
	}

	config := &Ip6ConfigData{}

	// This is a complex structure that needs reverse engineering
	// For now, store raw data and implement parsing based on actual data analysis
	config.InterfaceId = data

	return config, nil
}

// NewNetworkDeviceList creates a NetworkDeviceList from raw bytes.
func NewNetworkDeviceList(data []byte) (*NetworkDeviceList, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("NDL data too short")
	}

	ndl := &NetworkDeviceList{}

	// Parse based on the actual data structure
	// The _NDL variable appears to contain device path and MAC address info

	// Version or header info
	if len(data) >= 4 {
		ndl.Version = binary.LittleEndian.Uint32(data[0:4])
	}

	// Extract MAC address if present in known format
	for i := 0; i <= len(data)-6; i++ {
		// Look for MAC address patterns
		if i+6 <= len(data) {
			mac := data[i : i+6]
			// Simple heuristic: if bytes look like a MAC, treat as such
			if isValidMACPattern(mac) {
				entry := NetworkDeviceEntry{
					MacAddress: net.HardwareAddr(mac),
				}
				ndl.Entries = append(ndl.Entries, entry)
			}
		}
	}

	return ndl, nil
}

// isValidMACPattern checks if bytes could represent a MAC address.
func isValidMACPattern(data []byte) bool {
	if len(data) != 6 {
		return false
	}

	// Check if it's not all zeros or all 0xFF
	allZero := true
	allFF := true
	for _, b := range data {
		if b != 0 {
			allZero = false
		}
		if b != 0xFF {
			allFF = false
		}
	}

	return !allZero && !allFF
}

// NewPlatformConfig creates PlatformConfig from multiple variables.
func NewPlatformConfig() *PlatformConfig {
	return &PlatformConfig{}
}

// SetCpuClock sets CPU clock configuration.
func (pc *PlatformConfig) SetCpuClock(data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("invalid CPU clock data length")
	}
	pc.CpuClock = binary.LittleEndian.Uint32(data)
	return nil
}

// SetCustomCpuClock sets custom CPU clock configuration.
func (pc *PlatformConfig) SetCustomCpuClock(data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("invalid custom CPU clock data length")
	}
	pc.CustomCpuClock = binary.LittleEndian.Uint32(data)
	return nil
}

// SetRamMoreThan3GB sets RAM configuration.
func (pc *PlatformConfig) SetRamMoreThan3GB(data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("invalid RAM config data length")
	}
	pc.RamMoreThan3GB = binary.LittleEndian.Uint32(data) != 0
	return nil
}

// SetRamLimitTo3GB sets RAM limit configuration.
func (pc *PlatformConfig) SetRamLimitTo3GB(data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("invalid RAM limit data length")
	}
	pc.RamLimitTo3GB = binary.LittleEndian.Uint32(data) != 0
	return nil
}

// NewConsoleConfig creates ConsoleConfig from console variables.
func NewConsoleConfig() *ConsoleConfig {
	return &ConsoleConfig{}
}

// SetConsolePref sets console preference.
func (cc *ConsoleConfig) SetConsolePref(data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("invalid console pref data length")
	}
	cc.ConsolePref = binary.LittleEndian.Uint32(data)
	return nil
}

// NewSecurityConfig creates SecurityConfig from security variables.
func NewSecurityConfig() *SecurityConfig {
	return &SecurityConfig{}
}

// SetCustomMode sets custom mode state.
func (sc *SecurityConfig) SetCustomMode(data []byte) error {
	if len(data) != 1 {
		return fmt.Errorf("invalid custom mode data length")
	}
	sc.CustomMode = data[0] != 0
	return nil
}

// NewTimeConfig creates TimeConfig from time variables.
func NewTimeConfig() *TimeConfig {
	return &TimeConfig{}
}

// SetRtcEpochSeconds sets RTC epoch seconds.
func (tc *TimeConfig) SetRtcEpochSeconds(data []byte) error {
	if len(data) != 8 {
		return fmt.Errorf("invalid RTC epoch data length")
	}
	tc.RtcEpochSeconds = binary.LittleEndian.Uint64(data)
	return nil
}

// GetTimestamp returns the RTC time as a Go time.Time.
func (tc *TimeConfig) GetTimestamp() time.Time {
	return time.Unix(int64(tc.RtcEpochSeconds), 0).UTC()
}

// NewKeyData creates KeyData from key variable bytes.
func NewKeyData(data []byte) (*KeyData, error) {
	if len(data) < 10 {
		return nil, fmt.Errorf("key data too short")
	}

	kd := &KeyData{
		KeyCode:    binary.LittleEndian.Uint32(data[0:4]),
		ScanCode:   binary.LittleEndian.Uint16(data[4:6]),
		ShiftState: binary.LittleEndian.Uint32(data[6:10]),
	}

	return kd, nil
}

// String returns a string representation of the key data.
func (kd *KeyData) String() string {
	return fmt.Sprintf("Key: code=0x%08x, scan=0x%04x, shift=0x%08x",
		kd.KeyCode, kd.ScanCode, kd.ShiftState)
}

// NewAssetTag creates AssetTag from asset tag data.
func NewAssetTag(data []byte) (*AssetTag, error) {
	// Asset tag is typically a null-terminated string
	tag := string(data)
	// Remove null terminators
	for i, b := range data {
		if b == 0 {
			tag = string(data[:i])
			break
		}
	}

	return &AssetTag{Tag: tag}, nil
}

// String returns the asset tag string.
func (at *AssetTag) String() string {
	return at.Tag
}

// NewCertDatabase creates CertDatabase from certificate data.
func NewCertDatabase(data []byte) (*CertDatabase, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("cert database too short")
	}

	db := &CertDatabase{
		Version: binary.LittleEndian.Uint32(data[0:4]),
	}

	// Certificate parsing would require understanding the specific format
	// For now, store the raw data

	return db, nil
}
