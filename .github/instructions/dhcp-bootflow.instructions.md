# Netboot Design

We will support two primary boot options - EFI and Legacy Boot.

iPXE is used with UEFI boot and must provide an EFI payload via the kernel/initramfs bundle.

Extlinux/Syslinux is used for legacy BIOS boot and must provide a payload that supports the BOOT flag.

## State Machine

When we initially receive a DHCP request request, we should check the following:

- Is this a PXE client?
  - If true, check if it supports chainloading to iPXE (UEFI)
    - If true, provide the iPXE EFI payload.
    - If false, provide the Syslinux/Extlinux payload.
  - If false, we may ignore the request - this is not a netboot client.
- Is this an iPXE client?
  - If true, we can provide the iPXE EFI payload.
  - If false, we may ignore the request - this is not a netboot client.
- Is this a Tinkerbell client?
  - If true, we can provide the Tinkerbell payload.
  - If false, we may ignore the request - this is not a netboot client.


## Syslinux/Extlinux

In the event that we are providing a Syslinux/Extlinux payload, we should set the necessary DHCP options to route the next request to the Syslinux template. 

```go
  // Set option 209 - PXE Linux Configuration File
	// used by pxelinux to determine the config file to use.
	// useful with U-Boot and pxelinux.
	reply.UpdateOption(i.PxeLinuxConfigFileOption(i.Mac))
```

DHCP info may contain information regarding the interface hardware type, which will be used for the 
first hex position when crafting the PXE boot file name. For example, if the hardware type is 1 (Ethernet), the first hex position will be 01. If the hardware type is 6 (IEEE 802.5), the first hex position will be 06.

```go
// PxeLinuxConfigFileOption returns the PXE Linux config file option (option 209).
// The format is "pxelinux.cfg/<hardware-type>-<mac-address>" where hardware-type
// is a two-digit hex value from the DHCP packet's HWType field (e.g., "01" for Ethernet).
// See RFC 2132 and https://www.syslinux.org/wiki/index.php?title=PXELINUX for details.
func (i Info) PxeLinuxConfigFileOption(mac net.HardwareAddr) dhcpv4.Option {
	if mac == nil {
		mac = i.Mac
	}
	// Default to Ethernet (01) if packet is not available
	hwType := uint16(1)
	if i.Pkt != nil {
		hwType = uint16(i.Pkt.HWType)
	}
	filename := fmt.Sprintf("pxelinux.cfg/%02x-%s", hwType, macAddrFormat(mac, constant.MacAddrFormatDash))
	return dhcpv4.OptGeneric(dhcpv4.OptionPXELinuxConfigFile, []byte(filename))
}
```

When the desired payload is PXE Linux, we should set the following DHCP options to route the next request to the Syslinux template.

```go
  OptionTFTPServerAddress    optionCode = 150
  OptionPXELinuxMagicString  optionCode = 208
	OptionPXELinuxConfigFile   optionCode = 209
	OptionPXELinuxPathPrefix   optionCode = 210
	OptionPXELinuxRebootTime   optionCode = 211
  OptionPXELinuxServicePath  optionCode = 212
```

In the event that we are providing an iPXE EFI payload, we should set the necessary DHCP options to route the next request to the iPXE template.

```go
  // Set option 210 - iPXE Boot Script
  // used by iPXE to determine the script file to use.
  reply.UpdateOption(i.IPXEBootScriptOption(i.Mac))
```
