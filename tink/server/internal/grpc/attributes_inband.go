package grpc

import (
	"strconv"

	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/inventory"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

// inBandAttributesFromAgent converts the Agent-reported attributes (as sent
// over gRPC) into the shape shared with the out-of-band collection path
// (status.attributes.inBand). It's a reshaping of data already collected by
// the Agent, not a superset: some Attributes fields have no analog in
// data.AgentAttributes today (e.g. per-DIMM memory detail, drive firmware/SMART
// status, network port link state) and are simply left unset. CollectionMethod
// and LastUpdated aren't Agent-collected fields, so they're not set here; the
// caller fills them in at write time.
func inBandAttributesFromAgent(attrs *data.AgentAttributes) *inventory.Attributes {
	if attrs == nil {
		return nil
	}

	out := &inventory.Attributes{
		Chassis:   chassisFromAgent(attrs.Chassis),
		BIOS:      biosFromAgent(attrs.BIOS),
		Baseboard: baseboardFromAgent(attrs.Baseboard),
		Product:   productFromAgent(attrs.Product),
	}

	out.CPU = cpuFromAgent(attrs.CPU)
	out.Memory = memoryFromAgent(attrs.Memory)

	for _, b := range attrs.BlockDevices {
		if b == nil {
			continue
		}
		out.BlockDevices = append(out.BlockDevices, blockDeviceFromAgent(b))
	}

	for _, n := range attrs.NetworkInterfaces {
		if n == nil {
			continue
		}
		out.NetworkInterfaces = append(out.NetworkInterfaces, networkInterfaceFromAgent(n))
	}

	for _, p := range attrs.PCIDevices {
		if p == nil {
			continue
		}
		out.PCIDevices = append(out.PCIDevices, pciDeviceFromAgent(p))
	}

	for _, g := range attrs.GPUDevices {
		if g == nil {
			continue
		}
		out.GPUDevices = append(out.GPUDevices, gpuDeviceFromAgent(g))
	}

	return out
}

func cpuFromAgent(cpu *data.CPU) *inventory.CPU {
	if cpu == nil {
		return nil
	}
	out := &inventory.CPU{}
	if cpu.TotalCores != nil {
		out.TotalCores = *cpu.TotalCores
	}
	if cpu.TotalThreads != nil {
		out.TotalThreads = *cpu.TotalThreads
	}
	for _, p := range cpu.Processors {
		if p == nil {
			continue
		}
		out.Sockets = append(out.Sockets, cpuSocketFromAgent(p))
	}
	return out
}

// cpuSocketFromAgent maps a single Agent-reported processor. Slot carries the
// Agent's numeric processor ID as a string - the same field out-of-band uses
// for a physical socket label (e.g. "CPU1") - since the Agent has no concept
// of a physical socket label of its own.
func cpuSocketFromAgent(p *data.Processor) inventory.CPUSocket {
	socket := inventory.CPUSocket{Capabilities: p.Capabilities}
	if p.ID != nil {
		socket.Slot = strconv.FormatUint(uint64(*p.ID), 10)
	}
	if p.Vendor != nil {
		socket.Vendor = *p.Vendor
	}
	if p.Model != nil {
		socket.Model = *p.Model
	}
	if p.Cores != nil {
		socket.Cores = *p.Cores
	}
	if p.Threads != nil {
		socket.Threads = *p.Threads
	}
	return socket
}

func memoryFromAgent(mem *data.Memory) *inventory.Memory {
	if mem == nil {
		return nil
	}
	out := &inventory.Memory{}
	if mem.TotalBytes != nil {
		out.TotalBytes = *mem.TotalBytes
	}
	if mem.UsableBytes != nil {
		out.UsableBytes = *mem.UsableBytes
	}
	return out
}

func blockDeviceFromAgent(b *data.Block) inventory.BlockDevice {
	dev := inventory.BlockDevice{}
	if b.Name != nil {
		dev.Name = *b.Name
	}
	if b.ControllerType != nil {
		dev.ControllerType = *b.ControllerType
	}
	if b.DriveType != nil {
		dev.DriveType = *b.DriveType
	}
	if b.Vendor != nil {
		dev.Vendor = *b.Vendor
	}
	if b.Model != nil {
		dev.Model = *b.Model
	}
	if b.SerialNumber != nil {
		dev.SerialNumber = *b.SerialNumber
	}
	if b.WWN != nil {
		dev.WWN = *b.WWN
	}
	if b.SizeBytes != nil {
		dev.SizeBytes = *b.SizeBytes
	}
	if b.PhysicalBlockSizeBytes != nil {
		dev.PhysicalBlockSizeBytes = *b.PhysicalBlockSizeBytes
	}
	if b.Path != nil {
		dev.Path = *b.Path
	}
	return dev
}

func pciDeviceFromAgent(p *data.PCI) inventory.PCIDevice {
	dev := inventory.PCIDevice{}
	if p.Vendor != nil {
		dev.Vendor = *p.Vendor
	}
	if p.Product != nil {
		dev.Model = *p.Product
	}
	if p.Class != nil {
		dev.Class = *p.Class
	}
	if p.Driver != nil {
		dev.Driver = *p.Driver
	}
	return dev
}

func gpuDeviceFromAgent(g *data.GPU) inventory.GPUDevice {
	dev := inventory.GPUDevice{}
	if g.Vendor != nil {
		dev.Vendor = *g.Vendor
	}
	if g.Product != nil {
		dev.Model = *g.Product
	}
	if g.Class != nil {
		dev.Class = *g.Class
	}
	if g.Driver != nil {
		dev.Driver = *g.Driver
	}
	return dev
}

// networkInterfaceFromAgent maps a single Agent-reported interface and its one
// synthesized port verbatim; the caller's PruneEmpty drops the port when it
// ends up empty (e.g. a name-only interface, so len(Ports)==0 still marks
// name-only), while a reported null "00:00:..." MAC is non-empty and preserved.
func networkInterfaceFromAgent(n *data.Network) inventory.NetworkInterface {
	iface := inventory.NetworkInterface{}
	if n.Name != nil {
		iface.Name = *n.Name
	}
	port := inventory.NetworkPort{EnabledCapabilities: n.EnabledCapabilities}
	if n.Mac != nil {
		port.MAC = *n.Mac
	}
	if n.SpeedMbps != nil {
		port.SpeedMbps = *n.SpeedMbps
	}
	for _, nb := range n.LLDPNeighbors {
		if nb == nil {
			continue
		}
		port.LLDPNeighbors = append(port.LLDPNeighbors, lldpNeighborFromAgent(nb))
	}
	iface.Ports = []inventory.NetworkPort{port}
	return iface
}

func lldpNeighborFromAgent(n *data.LLDPNeighbor) inventory.LLDPNeighbor {
	var out inventory.LLDPNeighbor
	if n.ChassisID != nil {
		out.ChassisID = *n.ChassisID
	}
	if n.SystemName != nil {
		out.SystemName = *n.SystemName
	}
	if n.PortID != nil {
		out.PortID = *n.PortID
	}
	if n.PortDescription != nil {
		out.PortDescription = *n.PortDescription
	}
	return out
}

func chassisFromAgent(c *data.Chassis) *inventory.Chassis {
	if c == nil {
		return nil
	}
	out := &inventory.Chassis{}
	if c.Vendor != nil {
		out.Vendor = *c.Vendor
	}
	if c.Serial != nil {
		out.SerialNumber = *c.Serial
	}
	return out
}

func biosFromAgent(b *data.BIOS) *inventory.BIOS {
	if b == nil {
		return nil
	}
	out := &inventory.BIOS{}
	if b.Vendor != nil {
		out.Vendor = *b.Vendor
	}
	if b.Version != nil {
		out.FirmwareVersion = *b.Version
	}
	if b.ReleaseDate != nil {
		out.ReleaseDate = *b.ReleaseDate
	}
	return out
}

func baseboardFromAgent(b *data.Baseboard) *inventory.Baseboard {
	if b == nil {
		return nil
	}
	out := &inventory.Baseboard{}
	if b.Vendor != nil {
		out.Vendor = *b.Vendor
	}
	if b.Product != nil {
		out.Model = *b.Product
	}
	if b.Version != nil {
		out.FirmwareVersion = *b.Version
	}
	if b.SerialNumber != nil {
		out.SerialNumber = *b.SerialNumber
	}
	return out
}

func productFromAgent(p *data.Product) *inventory.Product {
	if p == nil {
		return nil
	}
	out := &inventory.Product{}
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.Vendor != nil {
		out.Vendor = *p.Vendor
	}
	if p.SerialNumber != nil {
		out.SerialNumber = *p.SerialNumber
	}
	return out
}
