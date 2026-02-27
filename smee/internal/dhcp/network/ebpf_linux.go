//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/go-logr/logr"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const (
	// Veth pair names used by the eBPF manager.
	ebpfContainerVeth = "ebpf0"
	ebpfHostVeth      = "ebpf0-host"

	// TC handle for the eBPF filter.
	tcFilterHandle = 1

	// ETH_P_ALL for TC filter protocol.
	ethPAll = 0x0003
)

// EBPFManager uses an eBPF TC program to selectively redirect DHCP broadcast
// packets from the host's physical interface to a veth pair connected to the
// container namespace. Only UDP destination port 67 (DHCP server) packets are
// redirected; all other traffic passes through the host stack normally.
type EBPFManager struct {
	srcInterface string
	log          logr.Logger
	hostNs       netns.NsHandle

	targetMap *ebpf.Map
	prog      *ebpf.Program
}

// newEBPFManager creates an eBPF-based DHCP interface manager.
func newEBPFManager(log logr.Logger) (*EBPFManager, error) {
	if log.GetSink() == nil {
		log = logr.Discard()
	}

	hostNs, err := netns.GetFromPid(1)
	if err != nil {
		return nil, fmt.Errorf("getting host network namespace: %w", err)
	}

	m := &EBPFManager{
		log:    log,
		hostNs: hostNs,
	}

	iface, err := m.defaultGatewayInterface()
	if err != nil {
		_ = hostNs.Close()
		return nil, fmt.Errorf("detecting default gateway interface: %w", err)
	}
	m.srcInterface = iface

	log.Info("ebpf network manager initialized", "srcInterface", m.srcInterface)
	return m, nil
}

// Setup creates a veth pair, loads the eBPF program, and attaches it to the
// host's source interface via TC ingress.
func (m *EBPFManager) Setup(_ context.Context) error {
	m.log.Info("setting up eBPF DHCP redirect", "srcInterface", m.srcInterface)

	if err := m.Cleanup(); err != nil {
		m.log.V(1).Info("cleanup of stale resources failed, continuing", "error", err)
	}

	if err := m.createVethPair(); err != nil {
		return fmt.Errorf("creating veth pair: %w", err)
	}

	if err := m.moveVethToContainer(); err != nil {
		return fmt.Errorf("moving veth to container: %w", err)
	}

	if err := m.configureContainerVeth(); err != nil {
		return fmt.Errorf("configuring container veth: %w", err)
	}

	var hostVethIndex int
	if err := m.inHostNs(func() error {
		link, err := netlink.LinkByName(ebpfHostVeth)
		if err != nil {
			return fmt.Errorf("finding host veth %s: %w", ebpfHostVeth, err)
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("bringing up host veth: %w", err)
		}
		hostVethIndex = link.Attrs().Index
		return nil
	}); err != nil {
		return err
	}

	if err := m.loadAndAttach(hostVethIndex); err != nil {
		return fmt.Errorf("loading eBPF program: %w", err)
	}

	m.log.Info("eBPF DHCP redirect ready",
		"srcInterface", m.srcInterface,
		"hostVeth", ebpfHostVeth,
		"containerVeth", ebpfContainerVeth)

	return nil
}

// Cleanup removes the TC filter, closes eBPF resources, and deletes the veth pair.
func (m *EBPFManager) Cleanup() error {
	var errs []error

	if m.hostNs != 0 {
		if err := m.inHostNs(func() error {
			srcLink, err := netlink.LinkByName(m.srcInterface)
			if err != nil {
				return nil
			}
			m.detachTC(srcLink.Attrs().Index)
			return nil
		}); err != nil {
			errs = append(errs, err)
		}
	}

	if m.prog != nil {
		if err := m.prog.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing ebpf program: %w", err))
		}
		m.prog = nil
	}
	if m.targetMap != nil {
		if err := m.targetMap.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing ebpf map: %w", err))
		}
		m.targetMap = nil
	}

	for _, name := range []string{ebpfContainerVeth, ebpfHostVeth} {
		if link, err := netlink.LinkByName(name); err == nil {
			if err := netlink.LinkDel(link); err != nil {
				errs = append(errs, fmt.Errorf("deleting veth %s: %w", name, err))
			} else {
				m.log.V(1).Info("deleted veth", "name", name)
			}
		}
	}

	if m.hostNs != 0 {
		if err := m.inHostNs(func() error {
			for _, name := range []string{ebpfContainerVeth, ebpfHostVeth} {
				if link, err := netlink.LinkByName(name); err == nil {
					if err := netlink.LinkDel(link); err != nil {
						errs = append(errs, fmt.Errorf("deleting host veth %s: %w", name, err))
					}
				}
			}
			return nil
		}); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Close releases the host namespace handle.
func (m *EBPFManager) Close() error {
	if m.hostNs != 0 {
		return m.hostNs.Close()
	}
	return nil
}

// createVethPair creates a veth pair in the host network namespace.
func (m *EBPFManager) createVethPair() error {
	return m.inHostNs(func() error {
		veth := &netlink.Veth{
			LinkAttrs: netlink.LinkAttrs{
				Name: ebpfHostVeth,
			},
			PeerName: ebpfContainerVeth,
		}
		if err := netlink.LinkAdd(veth); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("creating veth pair: %w", err)
		}
		m.log.V(1).Info("created veth pair in host namespace",
			"host", ebpfHostVeth,
			"container", ebpfContainerVeth)
		return nil
	})
}

// moveVethToContainer moves the container-side veth from host to container ns.
func (m *EBPFManager) moveVethToContainer() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNs, err := netns.Get()
	if err != nil {
		return fmt.Errorf("getting container namespace: %w", err)
	}
	defer containerNs.Close()

	if err := netns.Set(m.hostNs); err != nil {
		return fmt.Errorf("switching to host namespace: %w", err)
	}
	defer func() { _ = netns.Set(containerNs) }()

	link, err := netlink.LinkByName(ebpfContainerVeth)
	if err != nil {
		return fmt.Errorf("finding container veth in host ns: %w", err)
	}

	if err := netlink.LinkSetNsFd(link, int(containerNs)); err != nil {
		return fmt.Errorf("moving veth to container namespace: %w", err)
	}

	m.log.V(1).Info("moved veth to container namespace", "name", ebpfContainerVeth)
	return nil
}

// configureContainerVeth brings up the container-side veth and assigns the DHCP address.
func (m *EBPFManager) configureContainerVeth() error {
	link, err := netlink.LinkByName(ebpfContainerVeth)
	if err != nil {
		return fmt.Errorf("finding veth %s in container: %w", ebpfContainerVeth, err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing veth up: %w", err)
	}

	addr, err := netlink.ParseAddr(dhcpIfAddr)
	if err != nil {
		return fmt.Errorf("parsing address %s: %w", dhcpIfAddr, err)
	}
	addr.Scope = 254 // RT_SCOPE_HOST

	if err := netlink.AddrAdd(link, addr); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("adding address to veth: %w", err)
	}

	m.log.V(1).Info("configured container veth",
		"interface", ebpfContainerVeth,
		"ip", dhcpIfAddr)
	return nil
}

// loadAndAttach creates the eBPF map/program and attaches the TC filter in
// the host namespace.
func (m *EBPFManager) loadAndAttach(hostVethIndex int) error {
	targetMap, err := ebpf.NewMap(&ebpf.MapSpec{
		Type:       ebpf.Array,
		KeySize:    4,
		ValueSize:  4,
		MaxEntries: 1,
	})
	if err != nil {
		return fmt.Errorf("creating ebpf map: %w", err)
	}

	key := uint32(0)
	val := uint32(hostVethIndex)
	if err := targetMap.Put(&key, &val); err != nil {
		_ = targetMap.Close()
		return fmt.Errorf("populating ebpf map with target ifindex %d: %w", hostVethIndex, err)
	}

	insns := dhcpRedirectProgram(targetMap.FD())

	prog, err := ebpf.NewProgram(&ebpf.ProgramSpec{
		Type:         ebpf.SchedCLS,
		Instructions: insns,
		License:      "GPL",
	})
	if err != nil {
		_ = targetMap.Close()
		return fmt.Errorf("loading ebpf program: %w", err)
	}

	m.targetMap = targetMap
	m.prog = prog

	return m.inHostNs(func() error {
		srcLink, err := netlink.LinkByName(m.srcInterface)
		if err != nil {
			return fmt.Errorf("finding source interface %s: %w", m.srcInterface, err)
		}
		return m.attachTC(srcLink.Attrs().Index)
	})
}

// attachTC adds a clsact qdisc and BPF filter to the source interface.
func (m *EBPFManager) attachTC(srcIfindex int) error {
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: srcIfindex,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}
	if err := netlink.QdiscAdd(qdisc); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("adding clsact qdisc: %w", err)
	}

	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: srcIfindex,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Handle:    tcFilterHandle,
			Protocol:  ethPAll,
		},
		Fd:           m.prog.FD(),
		Name:         "dhcp_redirect",
		DirectAction: true,
	}

	if err := netlink.FilterReplace(filter); err != nil {
		return fmt.Errorf("attaching ebpf TC filter: %w", err)
	}

	m.log.V(1).Info("attached eBPF TC filter",
		"interface", m.srcInterface,
		"ifindex", srcIfindex)
	return nil
}

// detachTC removes the BPF filter from the source interface.
func (m *EBPFManager) detachTC(srcIfindex int) {
	filters, err := netlink.FilterList(
		&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Index: srcIfindex}},
		netlink.HANDLE_MIN_INGRESS,
	)
	if err != nil {
		m.log.V(1).Info("listing TC filters failed", "error", err)
		return
	}

	for i := range filters {
		bf, ok := filters[i].(*netlink.BpfFilter)
		if !ok {
			continue
		}
		if bf.Name == "dhcp_redirect" {
			if err := netlink.FilterDel(bf); err != nil {
				m.log.V(1).Info("deleting TC filter failed", "error", err)
			} else {
				m.log.V(1).Info("removed eBPF TC filter", "ifindex", srcIfindex)
			}
		}
	}
}

// dhcpRedirectProgram builds BPF assembly that parses Ethernet → IPv4 → UDP
// headers and redirects packets destined to UDP port 67 (DHCP server) to the
// veth host-side interface. Non-matching packets return TC_ACT_OK (pass).
func dhcpRedirectProgram(mapFD int) asm.Instructions {
	return asm.Instructions{
		// === 1. Load packet pointers ===
		asm.LoadMem(asm.R2, asm.R1, 76, asm.Word), // R2 = skb->data
		asm.LoadMem(asm.R3, asm.R1, 80, asm.Word), // R3 = skb->data_end

		// === 2. Ethernet header (14 bytes) ===
		asm.Mov.Reg(asm.R5, asm.R2),
		asm.Add.Imm(asm.R5, 14),
		asm.JGT.Reg(asm.R5, asm.R3, "pass"),

		asm.LoadMem(asm.R6, asm.R2, 12, asm.Half),
		asm.JNE.Imm(asm.R6, 0x0008, "pass"),

		// === 3. IPv4 header (min 20 bytes) ===
		asm.Mov.Reg(asm.R5, asm.R2),
		asm.Add.Imm(asm.R5, 34),
		asm.JGT.Reg(asm.R5, asm.R3, "pass"),

		asm.LoadMem(asm.R6, asm.R2, 23, asm.Byte),
		asm.JNE.Imm(asm.R6, 17, "pass"),

		asm.LoadMem(asm.R6, asm.R2, 14, asm.Byte),
		asm.And.Imm(asm.R6, 0x0F),
		asm.LSh.Imm(asm.R6, 2),

		// === 4. UDP header (8 bytes) ===
		asm.Mov.Reg(asm.R7, asm.R2),
		asm.Add.Imm(asm.R7, 14),
		asm.Add.Reg(asm.R7, asm.R6),

		asm.Mov.Reg(asm.R5, asm.R7),
		asm.Add.Imm(asm.R5, 8),
		asm.JGT.Reg(asm.R5, asm.R3, "pass"),

		asm.LoadMem(asm.R6, asm.R7, 2, asm.Half),
		asm.JNE.Imm(asm.R6, 0x4300, "pass"),

		// === 5. Map lookup & redirect ===
		asm.LoadMapPtr(asm.R1, mapFD),
		asm.Mov.Imm(asm.R2, 0),
		asm.StoreMem(asm.R10, -4, asm.R2, asm.Word),
		asm.Mov.Reg(asm.R2, asm.R10),
		asm.Add.Imm(asm.R2, -4),
		asm.FnMapLookupElem.Call(),

		asm.JEq.Imm(asm.R0, 0, "pass"),

		asm.LoadMem(asm.R1, asm.R0, 0, asm.Word),
		asm.JEq.Imm(asm.R1, 0, "pass"),

		asm.Mov.Imm(asm.R2, 0),
		asm.FnRedirect.Call(),
		asm.Return(),

		// === 6. Pass ===
		asm.Mov.Imm(asm.R0, 0).WithSymbol("pass"),
		asm.Return(),
	}
}

// defaultGatewayInterface returns the interface for the default route in the host namespace.
func (m *EBPFManager) defaultGatewayInterface() (string, error) {
	var ifName string
	err := m.inHostNs(func() error {
		routes, err := netlink.RouteList(nil, unix.AF_INET)
		if err != nil {
			return fmt.Errorf("listing routes: %w", err)
		}
		for _, route := range routes {
			if route.Dst == nil || route.Dst.String() == "0.0.0.0/0" {
				if route.LinkIndex > 0 {
					link, err := netlink.LinkByIndex(route.LinkIndex)
					if err != nil {
						continue
					}
					ifName = link.Attrs().Name
					return nil
				}
			}
		}
		return fmt.Errorf("no default gateway interface found")
	})
	return ifName, err
}

// inHostNs executes fn in the host network namespace.
func (m *EBPFManager) inHostNs(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNs, err := netns.Get()
	if err != nil {
		return fmt.Errorf("getting current namespace: %w", err)
	}
	defer origNs.Close()

	if err := netns.Set(m.hostNs); err != nil {
		return fmt.Errorf("switching to host namespace: %w", err)
	}
	defer func() { _ = netns.Set(origNs) }()

	return fn()
}
