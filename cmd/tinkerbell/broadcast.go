//go:build linux

package main

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// The DHCP conduit is an ipvlan (L2 mode) or macvlan (bridge mode) interface,
// attached to one of the host's physical interfaces and living in the pod's
// network namespace. Its only job is to carry DHCP: broadcasts from the physical
// segment arrive on it, and the replies the DHCP server writes back to it go out
// of the same physical port. It holds no address and installs no route, so
// nothing else in the pod ever uses it and the CNI datapath stays in sole charge
// of routed traffic. Both types are created, hardened and removed identically;
// --dhcp-conduit-type only chooses the link kind.
//
// ipvlan is the default. It shares its parent's MAC address, so attaching one
// adds no second unicast address for the parent to filter on. A macvlan gets its
// own MAC, and a NIC that cannot filter on more than one — the Cadence GEM (macb)
// on the Raspberry Pi 5's RP1 never programs its unicast list at all — can only
// receive that second address by being put into promiscuous mode. The kernel
// does that escalation silently, and on the GEM it also turns off hardware RX
// checksum offload, pushing every checksum onto the CPU that is already handling
// the interrupt.
//
// macvlan is there for the parent that is already taken. A netdev has a single
// rx_handler slot, so an ipvlan cannot attach to a parent that already carries a
// macvlan (or vice versa) and fails with EBUSY — but macvlans share the slot with
// each other. A NIC that already hosts a Multus macvlan NetworkAttachmentDefinition
// can therefore take a macvlan conduit alongside it, where an ipvlan conduit would
// have to displace it. See setupConduit for how a conflict is reported.

const (
	// conduitPrefix starts the conduit's name in both forms below.
	conduitPrefix = "tink-dhcp"

	// conduitLeaderName is the conduit's name when DHCP leader election is
	// enabled. Only the leader ever serves DHCP, and it creates the conduit on
	// acquiring the lease and removes it on losing it, so the conduit is a
	// singleton and a fixed name says so.
	conduitLeaderName = conduitPrefix + "0"

	// podUIDEnv holds the pod UID, supplied by the downward API, from which the
	// per-pod conduit name is derived when leader election is disabled and every
	// replica serves DHCP for itself.
	podUIDEnv = "MY_POD_UID"

	// conduitMTU is one byte below IPV6_MIN_MTU (1280). ipv6_add_dev refuses to
	// attach an inet6_dev to a link whose MTU is under that, so the conduit gets
	// no IPv6 stack at all: no link-local address, no all-nodes multicast
	// membership, and so nothing that could act on a router advertisement.
	//
	// This is what a privileged container used to buy. Disabling IPv6 the
	// conventional way means writing net.ipv6.conf.<if>.disable_ipv6, and the
	// kernel exposes no netlink equivalent (inet6_set_link_af accepts only
	// IFLA_INET6_TOKEN and IFLA_INET6_ADDR_GEN_MODE), so it needs a writable
	// /proc/sys — which container runtimes mount read-only for everything but a
	// privileged container. Capping the MTU needs only CAP_NET_ADMIN and is the
	// stronger guarantee besides: there is no IPv6 to misconfigure rather than
	// IPv6 configured to stay quiet.
	//
	// DHCP fits comfortably. RFC 2131 fixes the minimum any client must accept
	// at 576 bytes and clients that want more say so in option 57; Smee's replies
	// run to a few hundred bytes. Inbound frames are unaffected either way, as
	// ipvlan delivers them with netif_rx, which does not test the MTU.
	conduitMTU = 1279
)

// conduitNameRe matches the interface names this binary creates, in both forms:
// the fixed leader-elected name and the per-pod one.
var conduitNameRe = regexp.MustCompile(`^tink-dhcp(0|[0-9a-f]{6})$`)

// Kernel ABI constants from include/uapi/linux/if_link.h. They are stable and
// spelled out here rather than taken from x/sys/unix so that the header they
// come from, and their meaning, travel with the code that depends on them.
const (
	// iflaTargetNetnsID selects the namespace an RTM_GETLINK dump reports on.
	iflaTargetNetnsID = 46
	// iflaLinkNetnsID names the namespace IFLA_LINK's interface index belongs to.
	iflaLinkNetnsID = 37
	// iflaIPVlanMode carries the ipvlan mode inside IFLA_INFO_DATA.
	iflaIPVlanMode = 1
	// ipvlanModeL2 is IPVLAN_MODE_L2, the only mode that passes broadcast and
	// multicast through to the slave. L3 and L3S drop both, which would leave the
	// DHCP server deaf to every DHCPDISCOVER.
	ipvlanModeL2 = 0
	// iflaMacvlanMode carries the macvlan mode inside IFLA_INFO_DATA.
	iflaMacvlanMode = 1
	// macvlanModeBridge is MACVLAN_MODE_BRIDGE. Every mode receives broadcasts
	// from the parent's segment; bridge additionally lets macvlans on the same
	// parent reach each other directly, so a client attached through another
	// macvlan on this host — a VM or a Multus-attached pod — is heard as well.
	// vishvananda/netlink numbers its MACVLAN_MODE_* constants differently from
	// the kernel, so the kernel's value is spelled out here.
	macvlanModeBridge = 4
)

// conduitLink describes how to create one type of conduit. Everything that
// differs between the types is here; the rest of the conduit's life is common.
type conduitLink struct {
	// kind is the rtnetlink IFLA_INFO_KIND.
	kind string
	// data adds the type's IFLA_INFO_DATA attributes.
	data func(*nl.RtAttr)
	// mode describes the result for logs.
	mode string
	// rival is the link type that cannot share a parent with this one, named in
	// the explanation of an EBUSY.
	rival flag.ConduitType
}

var conduitLinks = map[flag.ConduitType]conduitLink{
	flag.ConduitTypeIPVlan: {
		kind:  "ipvlan",
		data:  func(d *nl.RtAttr) { d.AddRtAttr(iflaIPVlanMode, nl.Uint16Attr(ipvlanModeL2)) },
		mode:  "ipvlan-l2",
		rival: flag.ConduitTypeMacvlan,
	},
	flag.ConduitTypeMacvlan: {
		kind:  "macvlan",
		data:  func(d *nl.RtAttr) { d.AddRtAttr(iflaMacvlanMode, nl.Uint32Attr(macvlanModeBridge)) },
		mode:  "macvlan-bridge",
		rival: flag.ConduitTypeIPVlan,
	},
}

// conduitLinkFor returns how to create a conduit of the given type. An empty
// type is the default, ipvlan.
func conduitLinkFor(typ flag.ConduitType) (flag.ConduitType, conduitLink, error) {
	if typ == "" {
		typ = flag.ConduitTypeIPVlan
	}
	l, ok := conduitLinks[typ]
	if !ok {
		return "", conduitLink{}, fmt.Errorf("unknown DHCP conduit type %q, must be %q or %q", typ, flag.ConduitTypeIPVlan, flag.ConduitTypeMacvlan)
	}

	return typ, l, nil
}

// conduitIfaceName returns the name to give the DHCP conduit.
//
// Under leader election the conduit belongs to whichever instance holds the
// lease, is created when that instance acquires it and removed when it loses it,
// so one fixed name is correct and makes a leftover from a crashed leader
// recognisable. Without leader election every replica serves DHCP for itself and
// needs a name of its own, derived from the pod UID so that it is stable across
// container restarts within the same pod.
func conduitIfaceName(leaderElected bool) (string, error) {
	if leaderElected {
		return conduitLeaderName, nil
	}

	uid := os.Getenv(podUIDEnv)
	if uid == "" {
		return "", fmt.Errorf("env var %s is not set; it is required to name the DHCP conduit when leader election is disabled", podUIDEnv)
	}

	hex := strings.ToLower(strings.ReplaceAll(uid, "-", ""))
	if len(hex) < 6 {
		return "", fmt.Errorf("pod UID %q is too short after stripping dashes (got %d chars, need at least 6)", uid, len(hex))
	}

	name := conduitPrefix + hex[:6]
	if !conduitNameRe.MatchString(name) {
		return "", fmt.Errorf("pod UID %q does not yield a usable interface name (derived %q)", uid, name)
	}

	return name, nil
}

// setupConduit creates the DHCP conduit and brings it up, returning a function
// that removes it again.
//
// The interface is created directly into this network namespace: the kernel is
// told, in one RTM_NEWLINK, both which interface in the host namespace to attach
// to and that the result belongs here. That needs CAP_NET_ADMIN and nothing
// else — no entering the host namespace, and so no CAP_SYS_ADMIN, no hostPID,
// and no privileged container. It also means a failed creation leaves nothing
// behind in the host namespace for someone to clean up, because the interface
// never fully exists there.
//
// typ is the kind of link to create, ipvlan when empty. sourceIface is the host
// interface to attach to; when empty a single physical interface is looked for
// and used.
func setupConduit(log logr.Logger, typ flag.ConduitType, sourceIface, ifaceName string) (cleanup func(), err error) {
	typ, kind, err := conduitLinkFor(typ)
	if err != nil {
		return nil, err
	}

	// A pod's network namespace belongs to the sandbox and outlives the
	// container, so an ungraceful exit — an OOM kill, a SIGKILL after the grace
	// period, a panic past the deferred teardown — leaves the conduit behind
	// holding exactly the name this start needs.
	purgeStaleConduits(log, ifaceName)

	nsid, via, err := hostNetnsID()
	if err != nil {
		return nil, err
	}
	log.V(1).Info("resolved host network namespace", "nsid", nsid, "learnedFrom", via)

	links, err := hostLinks(nsid)
	if err != nil {
		return nil, err
	}

	if sourceIface == "" {
		sourceIface, err = detectHostParent(links)
		if err != nil {
			return nil, err
		}
		log.Info("auto-detected DHCP conduit parent interface", "interface", sourceIface)
	}

	parentIndex, err := hostLinkIndex(links, sourceIface)
	if err != nil {
		return nil, err
	}

	if err := addConduit(ifaceName, typ, parentIndex, nsid); err != nil {
		if errors.Is(err, unix.EBUSY) {
			return nil, fmt.Errorf("create %[1]s %[2]q on host interface %[3]q: %[4]w; a netdev has one rx_handler slot, so this is what the kernel reports when %[3]q already carries a %[5]s, or is a bridge, bond or Open vSwitch port — a Multus %[5]s NetworkAttachmentDefinition on the same NIC is the usual cause. "+
				"If it is a %[5]s, set --dhcp-conduit-type=%[5]s: %[5]ss share a parent with each other, so the conduit can attach alongside it",
				kind.kind, ifaceName, sourceIface, err, kind.rival)
		}

		return nil, fmt.Errorf("create %s %q on host interface %q: %w", kind.kind, ifaceName, sourceIface, err)
	}

	// From here the interface is in this namespace, which outlives the process.
	// Remove it on any later failure so a restart does not meet its own leftover.
	defer func() {
		if err != nil {
			teardownConduit(log, ifaceName)
		}
	}()

	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("find DHCP conduit %q after creating it: %w", ifaceName, err)
	}

	// Harden before the link comes up, so no traffic from the physical segment is
	// ever processed with the permissive defaults in place.
	if err := hardenConduit(log, link); err != nil {
		return nil, err
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return nil, fmt.Errorf("bring up DHCP conduit %q: %w", ifaceName, err)
	}

	log.Info("DHCP conduit ready",
		"interface", ifaceName, "parent", sourceIface, "mode", kind.mode,
		"mtu", conduitMTU, "addressed", false)

	return func() { teardownConduit(log, ifaceName) }, nil
}

// hostNetnsID returns the id this network namespace holds for the host's, and
// the interface it was learned from.
//
// The kernel allocates that id when a link crosses a namespace boundary and
// reports it thereafter as IFLA_LINK_NETNSID, so any pod whose CNI hands it one
// end of a veth pair — Cilium, Calico and flannel all do — already has one for
// the host namespace, recorded against the interface the CNI moved in.
//
// Having it is what lets the conduit be created with CAP_NET_ADMIN alone: it
// names the host namespace in an ordinary netlink message, so there is no need
// to enter that namespace with setns, which needs CAP_SYS_ADMIN, nor to reach a
// handle on it through /proc/1/ns/net, which needs hostPID and read access to
// PID 1.
func hostNetnsID() (int, string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return -1, "", fmt.Errorf("list interfaces in the pod network namespace: %w", err)
	}

	var seen []string
	for _, l := range links {
		attrs := l.Attrs()
		// Skip a conduit left over from an earlier run. It carries a namespace id
		// of its own and would answer correctly, but the CNI's interface is the
		// one that is always present and is the honest source.
		if attrs.Flags&net.FlagLoopback != 0 || conduitNameRe.MatchString(attrs.Name) {
			continue
		}
		seen = append(seen, fmt.Sprintf("%s(%s)", attrs.Name, l.Type()))
		if attrs.NetNsID >= 0 {
			return attrs.NetNsID, attrs.Name, nil
		}
	}

	return -1, "", fmt.Errorf("no interface in this pod records the host network namespace, so the DHCP conduit cannot name it "+
		"(interfaces seen: %s); with host networking the pod is already in the host's namespace, so bind the DHCP server straight to "+
		"a host interface with --dhcp-bind-interface instead of enabling the conduit, and otherwise this means the CNI did not move "+
		"an interface in from the host, which the conduit relies on", strings.Join(seen, ", "))
}

// hostLink is the subset of an interface's attributes needed to pick a parent.
type hostLink struct {
	index int
	name  string
	// kind is the rtnetlink link type: "veth", "bridge", "bond" and so on.
	// Physical interfaces have no rtnl_link_ops and so report nothing here,
	// which is what distinguishes them.
	kind    string
	up      bool
	running bool
	ether   bool
}

// hostLinks lists the interfaces in the network namespace identified by nsid.
//
// RTM_GETLINK is the only rtnetlink dump that takes a target namespace, so this
// is all that can be seen of the host without entering it: no addresses and no
// routes. detectHostParent works within that limit.
func hostLinks(nsid int) ([]hostLink, error) {
	// A namespace id is a signed 32 bit value on the wire, and a negative one
	// means "not assigned" rather than naming a namespace.
	if nsid < 0 || nsid > math.MaxInt32 {
		return nil, fmt.Errorf("host network namespace has out of range id %d", nsid)
	}

	req := nl.NewNetlinkRequest(unix.RTM_GETLINK, unix.NLM_F_DUMP)
	req.AddData(nl.NewIfInfomsg(unix.AF_UNSPEC))
	req.AddData(nl.NewRtAttr(iflaTargetNetnsID, nl.Uint32Attr(uint32(nsid))))

	msgs, err := req.Execute(unix.NETLINK_ROUTE, unix.RTM_NEWLINK)
	if err != nil {
		return nil, fmt.Errorf("list interfaces in the host network namespace (nsid %d): %w", nsid, err)
	}

	links := make([]hostLink, 0, len(msgs))
	for _, m := range msgs {
		msg := nl.DeserializeIfInfomsg(m)
		attrs, err := nl.ParseRouteAttr(m[msg.Len():])
		if err != nil {
			return nil, fmt.Errorf("parse host interface attributes: %w", err)
		}

		l := hostLink{
			index:   int(msg.Index),
			up:      msg.Flags&unix.IFF_UP != 0,
			running: msg.Flags&unix.IFF_LOWER_UP != 0,
			ether:   msg.Type == unix.ARPHRD_ETHER,
		}
		for _, a := range attrs {
			switch a.Attr.Type {
			case unix.IFLA_IFNAME:
				l.name = nlString(a.Value)
			case unix.IFLA_LINKINFO:
				sub, err := nl.ParseRouteAttr(a.Value)
				if err != nil {
					return nil, fmt.Errorf("parse host interface link info: %w", err)
				}
				for _, s := range sub {
					if s.Attr.Type == nl.IFLA_INFO_KIND {
						l.kind = nlString(s.Value)
					}
				}
			}
		}
		links = append(links, l)
	}

	return links, nil
}

// nlString reads a NUL-terminated netlink string attribute.
func nlString(value []byte) string {
	return strings.TrimRight(string(value), "\x00")
}

// hostLinkIndex returns the index of the named interface on the host.
func hostLinkIndex(links []hostLink, name string) (int, error) {
	names := make([]string, 0, len(links))
	for _, l := range links {
		if l.name == name {
			return l.index, nil
		}
		names = append(names, l.name)
	}

	return 0, fmt.Errorf("interface %q does not exist on the host (host interfaces: %s)", name, strings.Join(names, ", "))
}

// detectHostParent picks the host interface to attach the conduit to when the
// operator has not named one.
//
// Only the link dump is available across a namespace boundary, so the host's
// routing table — which is what "the interface facing the network" really
// means — cannot be consulted. What can be told apart is a physical interface
// from a virtual one: every virtual link type registers rtnl_link_ops and so
// reports a kind, while a real NIC reports none. A node with exactly one
// physical interface that is up and has carrier therefore has exactly one
// candidate, and anything less clear-cut is left to the operator rather than
// guessed at, since guessing wrong attaches the DHCP server to the wrong
// network.
func detectHostParent(links []hostLink) (string, error) {
	var candidates, describe []string
	for _, l := range links {
		if l.name == "" || !l.ether {
			continue
		}
		describe = append(describe, fmt.Sprintf("%s(kind=%q up=%t carrier=%t)", l.name, l.kind, l.up, l.running))
		if l.kind == "" && l.up && l.running {
			candidates = append(candidates, l.name)
		}
	}

	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", fmt.Errorf("no physical host interface is up with carrier, so the DHCP conduit's parent cannot be auto-detected; "+
			"name it explicitly with --dhcp-conduit-interface (host interfaces: %s)", strings.Join(describe, ", "))
	default:
		return "", fmt.Errorf("the host has %d physical interfaces up with carrier (%s), so the DHCP conduit's parent is ambiguous; "+
			"name it explicitly with --dhcp-conduit-interface", len(candidates), strings.Join(candidates, ", "))
	}
}

// addConduit creates the conduit as a link of the given type.
//
// IFLA_LINK names the parent by index and IFLA_LINK_NETNSID says which namespace
// that index belongs to. Given both, rtnl_newlink_create builds the interface in
// the parent's namespace and then moves it into the one the request came from,
// as a single operation: either the conduit ends up here or the kernel unwinds
// it entirely, so a failure cannot strand an interface on the host. This is
// generic rtnetlink behaviour, so ipvlan and macvlan are created the same way.
//
// vishvananda/netlink reads IFLA_LINK_NETNSID but never writes it, so the
// message is assembled by hand.
func addConduit(name string, typ flag.ConduitType, parentIndex, nsid int) error {
	link, ok := conduitLinks[typ]
	if !ok {
		return fmt.Errorf("unknown DHCP conduit type %q", typ)
	}
	if parentIndex <= 0 || parentIndex > math.MaxInt32 {
		return fmt.Errorf("parent interface has out of range index %d", parentIndex)
	}
	if nsid < 0 || nsid > math.MaxInt32 {
		return fmt.Errorf("host network namespace has out of range id %d", nsid)
	}

	req := nl.NewNetlinkRequest(unix.RTM_NEWLINK, unix.NLM_F_CREATE|unix.NLM_F_EXCL|unix.NLM_F_ACK)
	req.AddData(nl.NewIfInfomsg(unix.AF_UNSPEC))
	req.AddData(nl.NewRtAttr(unix.IFLA_IFNAME, nl.ZeroTerminated(name)))
	req.AddData(nl.NewRtAttr(unix.IFLA_LINK, nl.Uint32Attr(uint32(parentIndex))))
	req.AddData(nl.NewRtAttr(iflaLinkNetnsID, nl.Uint32Attr(uint32(nsid))))

	info := nl.NewRtAttr(unix.IFLA_LINKINFO, nil)
	info.AddRtAttr(nl.IFLA_INFO_KIND, nl.NonZeroTerminated(link.kind))
	link.data(info.AddRtAttr(nl.IFLA_INFO_DATA, nil))
	req.AddData(info)

	_, err := req.Execute(unix.NETLINK_ROUTE, 0)

	return err
}

// IPv4 devconf indexes from include/uapi/linux/ip.h. These are the attribute
// types the kernel expects inside IFLA_INET_CONF and are stable kernel ABI.
const (
	devconfAcceptRedirects = 4
	devconfSendRedirects   = 6
	devconfArpAnnounce     = 18
	devconfArpIgnore       = 19

	// iflaInetConf is IFLA_INET_CONF from include/uapi/linux/if_link.h.
	iflaInetConf = 1
)

// conduitDevconf are the per-interface IPv4 settings applied to the conduit.
//
// ARP requests are broadcast, and both an L2 ipvlan and a macvlan pass every
// broadcast to every slave, so requests for addresses the pod holds elsewhere —
// its CNI address on eth0 above all — do reach the conduit. At the default arp_ignore of 0 the pod
// would answer them, putting its cluster address on the physical segment and
// drawing traffic for it away from the CNI. arp_ignore=8 answers for nothing;
// arp_announce=2 keeps any request the interface does send from advertising an
// address that is not valid there. The redirect settings stop hosts on the
// segment steering the pod's traffic.
//
// The two ARP settings are the load-bearing ones and are guaranteed to take:
// the kernel resolves them as max(conf.all, conf.<if>) (IN_DEV_MAXCONF in
// include/linux/inetdevice.h), so a per-interface value always wins. The
// redirect settings are best effort by comparison — with forwarding disabled the
// kernel ORs them with conf.all, so a conf.all of 1 overrides them.
var conduitDevconf = map[int]uint32{
	devconfArpIgnore:       8,
	devconfArpAnnounce:     2,
	devconfAcceptRedirects: 0,
	devconfSendRedirects:   0,
}

// hardenConduit restrains the conduit to its one job, before it is brought up.
//
// Everything here goes over netlink and needs only the CAP_NET_ADMIN the
// interface was created with, so all of it is required to succeed; there is no
// best-effort half left over for a privileged container to complete.
func hardenConduit(log logr.Logger, link netlink.Link) error {
	name := link.Attrs().Name
	if !conduitNameRe.MatchString(name) {
		return fmt.Errorf("refusing to configure unexpected interface name %q", name)
	}

	// Below IPV6_MIN_MTU the kernel gives the link no IPv6 stack at all. See
	// conduitMTU.
	if err := netlink.LinkSetMTU(link, conduitMTU); err != nil {
		return fmt.Errorf("set DHCP conduit %q MTU to %d: %w", name, conduitMTU, err)
	}

	// Belt and braces behind the MTU: were the MTU ever raised, this still stops
	// the kernel generating a link-local address. It is not sufficient on its own
	// — an interface with no address of its own still joins the all-nodes
	// multicast group and still acts on a router advertisement, taking a global
	// address by SLAAC and installing an IPv6 default route that bypasses the CNI
	// — which is why the MTU carries the guarantee.
	if err := netlink.LinkSetIP6AddrGenMode(link, nl.IN6_ADDR_GEN_MODE_NONE); err != nil {
		log.V(1).Info("could not set IPv6 address generation mode; the MTU already denies this link an IPv6 stack",
			"interface", name, "err", err)
	}

	return setIPv4Devconf(link, conduitDevconf)
}

// setIPv4Devconf applies per-interface IPv4 settings over netlink, as
// RTM_SETLINK carrying IFLA_AF_SPEC -> AF_INET -> IFLA_INET_CONF.
//
// This is the same configuration /proc/sys/net/ipv4/conf/<if>/ exposes, but it
// needs only CAP_NET_ADMIN and works when /proc/sys is mounted read-only, which
// is how container runtimes mount it for unprivileged containers.
func setIPv4Devconf(link netlink.Link, values map[int]uint32) error {
	// ifinfomsg carries the index as a signed 32 bit field.
	index := link.Attrs().Index
	if index <= 0 || index > math.MaxInt32 {
		return fmt.Errorf("interface %q has out of range index %d", link.Attrs().Name, index)
	}

	req := nl.NewNetlinkRequest(unix.RTM_SETLINK, unix.NLM_F_ACK)

	msg := nl.NewIfInfomsg(unix.AF_UNSPEC)
	msg.Index = int32(index)
	req.AddData(msg)

	// NLA_F_NESTED is required on each level; without it the kernel rejects the
	// message with EINVAL.
	spec := nl.NewRtAttr(unix.IFLA_AF_SPEC|unix.NLA_F_NESTED, nil)
	inet := spec.AddRtAttr(unix.AF_INET|unix.NLA_F_NESTED, nil)
	conf := inet.AddRtAttr(iflaInetConf|unix.NLA_F_NESTED, nil)
	for cfg, value := range values {
		conf.AddRtAttr(cfg, nl.Uint32Attr(value))
	}
	req.AddData(spec)

	if _, err := req.Execute(unix.NETLINK_ROUTE, 0); err != nil {
		return fmt.Errorf("set IPv4 devconf on %q: %w", link.Attrs().Name, err)
	}

	return nil
}

// purgeStaleConduits removes every conduit already in this network namespace,
// leaving the one about to be created under the name wanted as the only one.
//
// The pod's network namespace outlives the container, so a conduit survives an
// ungraceful exit and holds the name the next start needs; the kernel would
// refuse to create the replacement with EEXIST, and without this the failure
// would repeat on every restart until the pod itself was deleted.
//
// Under leader election it does more than that. Acquiring the lease is the point
// at which this instance becomes the only one entitled to serve DHCP, and
// clearing every conduit here makes that true of the interfaces as well —
// including one under a name this build no longer uses, left by a previous
// version or a change of leader-election setting.
func purgeStaleConduits(log logr.Logger, wanted string) {
	links, err := netlink.LinkList()
	if err != nil {
		log.Error(err, "failed to list interfaces while clearing stale DHCP conduits")
		return
	}

	for _, l := range links {
		name := l.Attrs().Name
		if !conduitNameRe.MatchString(name) {
			continue
		}
		if name == wanted {
			log.Info("removing DHCP conduit left behind by a previous run", "interface", name)
		} else {
			log.Info("removing DHCP conduit under an unexpected name", "interface", name, "expected", wanted)
		}
		if err := netlink.LinkDel(l); err != nil {
			log.Error(err, "failed to remove stale DHCP conduit", "interface", name)
		}
	}
}

// teardownConduit removes the conduit from this network namespace.
//
// Under leader election this runs as soon as the DHCP server stops, so the
// conduit is gone before this instance campaigns again and well before another
// instance can take the lease.
func teardownConduit(log logr.Logger, ifaceName string) {
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		// Already gone, which is the normal path when the namespace is being torn
		// down around us.
		return
	}
	log.Info("removing DHCP conduit", "interface", ifaceName)
	if err := netlink.LinkDel(link); err != nil {
		log.Error(err, "failed to remove DHCP conduit", "interface", ifaceName)
	}
}

// defaultRouteInterface returns the name of the interface in this network
// namespace that has a default IPv4 gateway. It is the fallback the DHCP server
// binds to when the conduit is disabled and no interface was named, so that the
// kernel has a routable source address for broadcast replies.
//
// It matches routes the same way autoDetectPublicIpv4WithDefaultGateway does: a
// route counts when Dst is nil (the kernel omitted it) or when Dst is the zero
// network and Gw is non-nil, which distinguishes a default route from a
// connected one.
func defaultRouteInterface() (string, error) {
	routes, err := netlink.RouteList(nil, unix.AF_INET)
	if err != nil {
		return "", fmt.Errorf("list routes: %w", err)
	}
	for _, r := range routes {
		if r.Dst == nil || (r.Dst.IP.Equal(net.IPv4zero) && r.Gw != nil) {
			iface, err := net.InterfaceByIndex(r.LinkIndex)
			if err != nil {
				return "", fmt.Errorf("interface by index %d: %w", r.LinkIndex, err)
			}

			return iface.Name, nil
		}
	}

	return "", errors.New("no default IPv4 route found")
}
