package efi

import (
	"encoding/hex"
	"testing"
)

func TestNewIp6ConfigData(t *testing.T) {
	// Test data from the D83ADD5A4436 variable
	hexData := "df7ffd7a533703003400670008000000010000003000500004000000020000002c00740004000000030000000100000000000000da3addfffe5a4436"
	data, err := hex.DecodeString(hexData)
	if err != nil {
		t.Fatalf("Failed to decode hex data: %v", err)
	}

	config, err := NewIp6ConfigData(data)
	if err != nil {
		t.Fatalf("Failed to create IP6 config: %v", err)
	}

	if config == nil {
		t.Fatal("IP6 config should not be nil")
	}

	if len(config.InterfaceId) == 0 {
		t.Error("InterfaceId should not be empty")
	}
}

func TestNewNetworkDeviceList(t *testing.T) {
	// Test data from the _NDL variable
	hexData := "030b2500d83add5a44360000000000000000000000000000000000000000000000000000017fff0400"
	data, err := hex.DecodeString(hexData)
	if err != nil {
		t.Fatalf("Failed to decode hex data: %v", err)
	}

	ndl, err := NewNetworkDeviceList(data)
	if err != nil {
		t.Fatalf("Failed to create NDL: %v", err)
	}

	if ndl == nil {
		t.Fatal("NDL should not be nil")
	}

	// Should find the MAC address d8:3a:dd:5a:44:36 in the data
	expectedMAC := "d8:3a:dd:5a:44:36"
	found := false
	for _, entry := range ndl.Entries {
		if entry.MacAddress.String() == expectedMAC {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find MAC address %s in NDL entries", expectedMAC)
	}
}

func TestPlatformConfig(t *testing.T) {
	pc := NewPlatformConfig()

	// Test CPU clock setting
	cpuClockData := []byte{0x01, 0x00, 0x00, 0x00} // Little-endian 1
	err := pc.SetCpuClock(cpuClockData)
	if err != nil {
		t.Fatalf("Failed to set CPU clock: %v", err)
	}

	if pc.CpuClock != 1 {
		t.Errorf("Expected CPU clock 1, got %d", pc.CpuClock)
	}

	// Test custom CPU clock
	customClockData := []byte{0x08, 0x07, 0x00, 0x00} // Little-endian 1800 (0x708)
	err = pc.SetCustomCpuClock(customClockData)
	if err != nil {
		t.Fatalf("Failed to set custom CPU clock: %v", err)
	}

	if pc.CustomCpuClock != 1800 {
		t.Errorf("Expected custom CPU clock 1800, got %d", pc.CustomCpuClock)
	}

	// Test RAM configuration
	ramData := []byte{0x01, 0x00, 0x00, 0x00} // True
	err = pc.SetRamMoreThan3GB(ramData)
	if err != nil {
		t.Fatalf("Failed to set RAM config: %v", err)
	}

	if !pc.RamMoreThan3GB {
		t.Error("Expected RamMoreThan3GB to be true")
	}

	ramLimitData := []byte{0x00, 0x00, 0x00, 0x00} // False
	err = pc.SetRamLimitTo3GB(ramLimitData)
	if err != nil {
		t.Fatalf("Failed to set RAM limit: %v", err)
	}

	if pc.RamLimitTo3GB {
		t.Error("Expected RamLimitTo3GB to be false")
	}
}

func TestConsoleConfig(t *testing.T) {
	cc := NewConsoleConfig()

	consolePrefData := []byte{0x00, 0x00, 0x00, 0x00} // 0
	err := cc.SetConsolePref(consolePrefData)
	if err != nil {
		t.Fatalf("Failed to set console pref: %v", err)
	}

	if cc.ConsolePref != 0 {
		t.Errorf("Expected console pref 0, got %d", cc.ConsolePref)
	}
}

func TestSecurityConfig(t *testing.T) {
	sc := NewSecurityConfig()

	customModeData := []byte{0x00} // False
	err := sc.SetCustomMode(customModeData)
	if err != nil {
		t.Fatalf("Failed to set custom mode: %v", err)
	}

	if sc.CustomMode {
		t.Error("Expected CustomMode to be false")
	}

	customModeDataTrue := []byte{0x01} // True
	err = sc.SetCustomMode(customModeDataTrue)
	if err != nil {
		t.Fatalf("Failed to set custom mode: %v", err)
	}

	if !sc.CustomMode {
		t.Error("Expected CustomMode to be true")
	}
}

func TestTimeConfig(t *testing.T) {
	tc := NewTimeConfig()

	// Test RTC epoch seconds - example data
	rtcData := []byte{0x09, 0xd7, 0x8b, 0x68, 0x00, 0x00, 0x00, 0x00} // Little-endian
	err := tc.SetRtcEpochSeconds(rtcData)
	if err != nil {
		t.Fatalf("Failed to set RTC epoch: %v", err)
	}

	if tc.RtcEpochSeconds == 0 {
		t.Error("RTC epoch seconds should not be zero")
	}

	// Test timestamp conversion
	timestamp := tc.GetTimestamp()
	if timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}

	// Should be a reasonable date (after 2000)
	if timestamp.Year() < 2000 {
		t.Errorf("Timestamp year should be after 2000, got %d", timestamp.Year())
	}
}

func TestKeyData(t *testing.T) {
	// Test key data parsing
	keyData := []byte{
		0x40, 0x00, 0x00, 0x00, // KeyCode (little-endian)
		0x84, 0x93, // ScanCode (little-endian)
		0x7a, 0xb8, 0x07, 0x00, // ShiftState (little-endian)
		0x0b, 0x00, 0x00, 0x00, // Additional data
	}

	kd, err := NewKeyData(keyData)
	if err != nil {
		t.Fatalf("Failed to create key data: %v", err)
	}

	if kd.KeyCode != 0x40 {
		t.Errorf("Expected KeyCode 0x40, got 0x%x", kd.KeyCode)
	}

	if kd.ScanCode != 0x9384 {
		t.Errorf("Expected ScanCode 0x9384, got 0x%x", kd.ScanCode)
	}

	str := kd.String()
	if str == "" {
		t.Error("Key data string should not be empty")
	}
}

func TestAssetTag(t *testing.T) {
	// Test asset tag with null-terminated string
	assetData := []byte("TestAsset\x00\x00\x00\x00")

	at, err := NewAssetTag(assetData)
	if err != nil {
		t.Fatalf("Failed to create asset tag: %v", err)
	}

	if at.Tag != "TestAsset" {
		t.Errorf("Expected asset tag 'TestAsset', got '%s'", at.Tag)
	}
}

func TestCertDatabase(t *testing.T) {
	// Test certificate database
	certData := []byte{0x04, 0x00, 0x00, 0x00} // Version 4

	db, err := NewCertDatabase(certData)
	if err != nil {
		t.Fatalf("Failed to create cert database: %v", err)
	}

	if db.Version != 4 {
		t.Errorf("Expected version 4, got %d", db.Version)
	}
}

func TestIsValidMACPattern(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "Valid MAC",
			data:     []byte{0xd8, 0x3a, 0xdd, 0x5a, 0x44, 0x36},
			expected: true,
		},
		{
			name:     "All zeros (invalid)",
			data:     []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			expected: false,
		},
		{
			name:     "All 0xFF (invalid)",
			data:     []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			expected: false,
		},
		{
			name:     "Too short",
			data:     []byte{0xd8, 0x3a, 0xdd},
			expected: false,
		},
		{
			name:     "Too long",
			data:     []byte{0xd8, 0x3a, 0xdd, 0x5a, 0x44, 0x36, 0x00},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidMACPattern(tt.data)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v for data %x", tt.expected, result, tt.data)
			}
		})
	}
}
