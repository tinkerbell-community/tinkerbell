package efi

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
)

// DHCP6 DUID Types as defined in RFC 3315.
const (
	DUID_TYPE_LLT = 1 // DUID Based on Link-layer Address Plus Time
	DUID_TYPE_EN  = 2 // DUID Assigned by Vendor Based on Enterprise Number
	DUID_TYPE_LL  = 3 // DUID Based on Link-layer Address
)

// Hardware types for DUID-LLT and DUID-LL.
const (
	HWTYPE_ETHERNET = 1
	HWTYPE_IEEE802  = 6
)

// Dhcp6Duid represents a DHCP6 DUID (DHCP Unique Identifier).
type Dhcp6Duid struct {
	Type             uint16
	HardwareType     uint16
	EnterpriseId     uint32 // For DUID-EN
	Identifier       []byte // For DUID-EN and unknown types
	Time             uint32
	LinkLayerAddress net.HardwareAddr
}

// NewDhcp6Duid creates a new DHCP6 DUID from raw data.
func NewDhcp6Duid(data []byte) (*Dhcp6Duid, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("data too short for DHCP6 DUID")
	}

	duid := &Dhcp6Duid{
		Type: binary.LittleEndian.Uint16(data[:2]),
	}

	// Parse based on DUID type
	switch duid.Type {
	case DUID_TYPE_LLT:
		if len(data) < 8 {
			return nil, fmt.Errorf("data too short for DUID-LLT")
		}
		duid.HardwareType = binary.LittleEndian.Uint16(data[2:4])
		duid.Time = binary.LittleEndian.Uint32(data[4:8])
		if len(data) > 8 {
			duid.LinkLayerAddress = net.HardwareAddr(data[8:])
		}
	case DUID_TYPE_EN:
		if len(data) < 6 {
			return nil, fmt.Errorf("data too short for DUID-EN")
		}
		duid.EnterpriseId = binary.LittleEndian.Uint32(data[2:6])
		if len(data) > 6 {
			duid.Identifier = make([]byte, len(data)-6)
			copy(duid.Identifier, data[6:])
		}
	case DUID_TYPE_LL:
		if len(data) < 4 {
			return nil, fmt.Errorf("data too short for DUID-LL")
		}
		duid.HardwareType = binary.LittleEndian.Uint16(data[2:4])
		if len(data) > 4 {
			duid.LinkLayerAddress = net.HardwareAddr(data[4:])
		}
	default:
		// Unknown DUID type - store raw data
		if len(data) > 2 {
			duid.Identifier = make([]byte, len(data)-2)
			copy(duid.Identifier, data[2:])
		}
	}

	return duid, nil
}

// String returns a string representation of the DHCP6 DUID.
func (d *Dhcp6Duid) String() string {
	switch d.Type {
	case DUID_TYPE_LLT:
		return fmt.Sprintf("DUID-LLT: hw_type=%d, time=%d, ll_addr=%s",
			d.HardwareType, d.Time, d.LinkLayerAddress.String())
	case DUID_TYPE_EN:
		return fmt.Sprintf("DUID-EN: enterprise=%d, id=%s",
			d.EnterpriseId, hex.EncodeToString(d.Identifier))
	case DUID_TYPE_LL:
		return fmt.Sprintf("DUID-LL: hw_type=%d, ll_addr=%s",
			d.HardwareType, d.LinkLayerAddress.String())
	default:
		return fmt.Sprintf("DUID-Unknown: type=%d, data=%s",
			d.Type, hex.EncodeToString(d.Identifier))
	}
}

// GetMacAddress extracts MAC address from DUID if present.
func (d *Dhcp6Duid) GetMacAddress() net.HardwareAddr {
	switch d.Type {
	case DUID_TYPE_LLT, DUID_TYPE_LL:
		if d.HardwareType == HWTYPE_ETHERNET && len(d.LinkLayerAddress) == 6 {
			return d.LinkLayerAddress
		}
	}
	return nil
}

// Bytes returns the binary representation of the DHCP6 DUID.
func (d *Dhcp6Duid) Bytes() []byte {
	var buf []byte

	// Add type
	typeBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(typeBytes, d.Type)
	buf = append(buf, typeBytes...)

	switch d.Type {
	case DUID_TYPE_LLT:
		hwTypeBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(hwTypeBytes, d.HardwareType)
		buf = append(buf, hwTypeBytes...)

		timeBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(timeBytes, d.Time)
		buf = append(buf, timeBytes...)

		buf = append(buf, d.LinkLayerAddress...)
	case DUID_TYPE_EN:
		enterpriseBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(enterpriseBytes, d.EnterpriseId)
		buf = append(buf, enterpriseBytes...)

		buf = append(buf, d.Identifier...)
	case DUID_TYPE_LL:
		hwTypeBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(hwTypeBytes, d.HardwareType)
		buf = append(buf, hwTypeBytes...)

		buf = append(buf, d.LinkLayerAddress...)
	default:
		buf = append(buf, d.Identifier...)
	}

	return buf
}
