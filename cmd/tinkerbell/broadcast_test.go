//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestConduitIfaceName(t *testing.T) {
	tests := map[string]struct {
		leaderElected bool
		uid           string
		want          string
		wantErr       bool
	}{
		"leader elected is a fixed singleton":     {leaderElected: true, want: conduitLeaderName},
		"leader elected ignores an absent pod ID": {leaderElected: true, uid: "", want: conduitLeaderName},
		"per pod from the pod UID":                {uid: "550e8400-e29b-41d4-a716-446655440000", want: "tink-dhcp550e84"},
		"dashes are stripped":                     {uid: "5-5-0-e-8-4-0-0", want: "tink-dhcp550e84"},
		"uppercase is normalised":                 {uid: "550E8400-E29B", want: "tink-dhcp550e84"},
		"unset pod UID":                           {uid: "", wantErr: true},
		"pod UID too short":                       {uid: "550e8", wantErr: true},
		"dashes only":                             {uid: "------", wantErr: true},
		"non hex pod UID":                         {uid: "zzzzzz-1111", wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(podUIDEnv, tt.uid)

			got, err := conduitIfaceName(tt.leaderElected)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("conduitIfaceName(%t) = %q, want error", tt.leaderElected, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("conduitIfaceName(%t) error = %v", tt.leaderElected, err)
			}
			if got != tt.want {
				t.Fatalf("conduitIfaceName(%t) = %q, want %q", tt.leaderElected, got, tt.want)
			}
		})
	}
}

// Both name forms must match the pattern purgeStaleConduits uses to find
// leftovers. A mismatch would leak an interface in the pod netns on every
// restart, and the next start would fail with EEXIST for good.
func TestConduitNamesMatchPurgePattern(t *testing.T) {
	t.Setenv(podUIDEnv, "550e8400-e29b-41d4-a716-446655440000")

	for _, leaderElected := range []bool{true, false} {
		got, err := conduitIfaceName(leaderElected)
		if err != nil {
			t.Fatalf("conduitIfaceName(%t) error = %v", leaderElected, err)
		}
		if !conduitNameRe.MatchString(got) {
			t.Errorf("conduitIfaceName(%t) = %q, which conduitNameRe does not match", leaderElected, got)
		}
		// Linux caps interface names at IFNAMSIZ-1 = 15 bytes.
		if len(got) > 15 {
			t.Errorf("conduitIfaceName(%t) = %q is %d bytes, over the 15 byte limit", leaderElected, got, len(got))
		}
	}
}

// The two forms must be distinguishable, or a per-pod name could be mistaken for
// the leader-elected singleton.
func TestConduitNameFormsAreDistinct(t *testing.T) {
	t.Setenv(podUIDEnv, "00000000-0000-0000-0000-000000000000")

	perPod, err := conduitIfaceName(false)
	if err != nil {
		t.Fatalf("conduitIfaceName(false) error = %v", err)
	}
	if perPod == conduitLeaderName {
		t.Fatalf("a pod UID of all zeroes produced %q, the same name leader election uses", perPod)
	}
}

// conduitMTU is the whole of the IPv6 defence: ipv6_add_dev refuses to attach an
// inet6_dev below IPV6_MIN_MTU, so the link gets no IPv6 stack to misconfigure.
// Raising it to 1280 or above silently reopens SLAAC and router advertisements.
func TestConduitMTUDeniesIPv6(t *testing.T) {
	const ipv6MinMTU = 1280 // IPV6_MIN_MTU, include/net/ipv6.h

	if conduitMTU >= ipv6MinMTU {
		t.Fatalf("conduitMTU = %d, must be below IPV6_MIN_MTU (%d) or the link gets an IPv6 stack", conduitMTU, ipv6MinMTU)
	}
	// DHCP clients must accept at least 576 bytes (RFC 2131 section 2), so the
	// MTU must leave room for that plus IP and UDP headers.
	if conduitMTU < 576 {
		t.Fatalf("conduitMTU = %d, too small to carry a 576 byte DHCP message", conduitMTU)
	}
}

// TestConduitDevconfUsesKernelABIIndexes pins the IPv4 devconf attribute types.
// They are array indexes into the kernel's ipv4_devconf, not names, so a wrong
// value silently configures a different setting instead of failing.
func TestConduitDevconfUsesKernelABIIndexes(t *testing.T) {
	// From include/uapi/linux/ip.h.
	want := map[int]uint32{
		4:  0, // IPV4_DEVCONF_ACCEPT_REDIRECTS
		6:  0, // IPV4_DEVCONF_SEND_REDIRECTS
		18: 2, // IPV4_DEVCONF_ARP_ANNOUNCE
		19: 8, // IPV4_DEVCONF_ARP_IGNORE
	}

	if len(conduitDevconf) != len(want) {
		t.Fatalf("conduitDevconf has %d entries, want %d", len(conduitDevconf), len(want))
	}
	for cfg, value := range want {
		got, ok := conduitDevconf[cfg]
		if !ok {
			t.Errorf("conduitDevconf is missing devconf index %d", cfg)
			continue
		}
		if got != value {
			t.Errorf("conduitDevconf[%d] = %d, want %d", cfg, got, value)
		}
	}

	// arp_ignore=8 means "do not reply for any local address". An L2 ipvlan
	// passes every broadcast to every slave, ARP requests included, so anything
	// lower answers for the pod's CNI address on the physical segment.
	if conduitDevconf[devconfArpIgnore] != 8 {
		t.Errorf("arp_ignore = %d, want 8", conduitDevconf[devconfArpIgnore])
	}
}

func TestHardenConduitRejectsUnexpectedName(t *testing.T) {
	// The name gates every configuration step, so anything this binary did not
	// generate must be refused rather than reconfigured. The link is never
	// touched, so an unattached value carrying only the name is enough.
	for _, name := range []string{"", "eth0", "lo", "tink-dhcp", "tink-dhcpZZZZZZ", "TINK-DHCP0", "../../etc/passwd"} {
		link := &netlink.IPVlan{LinkAttrs: netlink.LinkAttrs{Name: name}}
		if err := hardenConduit(logr.Discard(), link); err == nil {
			t.Errorf("hardenConduit(%q) = nil, want error", name)
		}
	}
}

// netnsIDForSelf allocates a namespace id for this process's own network
// namespace. Every cross-namespace operation the conduit performs is addressed
// by such an id, so pointing one at the current namespace exercises the real
// netlink messages — the same ones sent in production, byte for byte — inside a
// single throwaway namespace.
func netnsIDForSelf(t *testing.T) int {
	t.Helper()

	f, err := os.Open("/proc/self/ns/net")
	if err != nil {
		t.Skipf("cannot open own netns: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	nsid, err := netlink.GetNetNsIdByFd(int(f.Fd()))
	if err != nil || nsid < 0 {
		if err := netlink.SetNetNsIdByFd(int(f.Fd()), 0); err != nil {
			t.Skipf("cannot allocate a netns id (needs CAP_NET_ADMIN in a private netns): %v", err)
		}
		if nsid, err = netlink.GetNetNsIdByFd(int(f.Fd())); err != nil {
			t.Fatalf("reading back the netns id: %v", err)
		}
	}

	return nsid
}

// mustCreateParent makes a stand-in for the host's physical NIC. dummy is used
// because, like the Cadence GEM this design exists for, it advertises no
// IFF_UNICAST_FLT.
func mustCreateParent(t *testing.T, name string) netlink.Link {
	t.Helper()

	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name}}); err != nil {
		t.Skipf("cannot create a test interface (needs CAP_NET_ADMIN in a private netns, e.g. under `unshare -rn`): %v", err)
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("LinkByName(%q) error = %v", name, err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatalf("bringing up %q: %v", name, err)
	}

	return link
}

// TestAddConduitOverNetlink exercises the hand-assembled RTM_NEWLINK. Its layout
// is easy to get subtly wrong — a missing IFLA_LINK_NETNSID, the mode in the
// wrong nesting level — and nothing else in the suite would catch that.
func TestAddConduitOverNetlink(t *testing.T) {
	const parentName, conduitName = "tinkparent0", "tink-dhcp0"

	parent := mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	if err := addConduit(conduitName, parent.Attrs().Index, nsid); err != nil {
		t.Fatalf("addConduit() error = %v", err)
	}
	link, err := netlink.LinkByName(conduitName)
	if err != nil {
		t.Fatalf("conduit not in this netns after creation: %v", err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	if got := link.Type(); got != "ipvlan" {
		t.Errorf("conduit type = %q, want %q", got, "ipvlan")
	}
	if got, ok := link.(*netlink.IPVlan); ok && got.Mode != netlink.IPVLAN_MODE_L2 {
		t.Errorf("conduit mode = %v, want IPVLAN_MODE_L2; L3 and L3S drop broadcast and would make the DHCP server deaf", got.Mode)
	}

	// Sharing the parent's MAC is the entire reason for choosing ipvlan: it is
	// what stops the kernel escalating the parent to promiscuous mode.
	if got, want := link.Attrs().HardwareAddr.String(), parent.Attrs().HardwareAddr.String(); got != want {
		t.Errorf("conduit MAC = %s, want the parent's %s", got, want)
	}
}

// The parent must not end up in promiscuous mode. That escalation is what breaks
// the Raspberry Pi 5's RP1 NIC, and it is the whole reason this is an ipvlan.
func TestConduitLeavesParentUnpromiscuous(t *testing.T) {
	const parentName, conduitName = "tinkparent1", "tink-dhcp0"

	parent := mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	if err := addConduit(conduitName, parent.Attrs().Index, nsid); err != nil {
		t.Fatalf("addConduit() error = %v", err)
	}
	link, err := netlink.LinkByName(conduitName)
	if err != nil {
		t.Fatalf("LinkByName(%q) error = %v", conduitName, err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	if err := hardenConduit(logr.Discard(), link); err != nil {
		t.Fatalf("hardenConduit() error = %v", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatalf("bringing up the conduit: %v", err)
	}

	after, err := netlink.LinkByName(parentName)
	if err != nil {
		t.Fatalf("re-reading the parent: %v", err)
	}
	if after.Attrs().Promisc != 0 {
		t.Errorf("parent %q promisc count = %d, want 0", parentName, after.Attrs().Promisc)
	}
	if after.Attrs().RawFlags&unix.IFF_PROMISC != 0 {
		t.Errorf("parent %q has IFF_PROMISC set", parentName)
	}
}

// TestHardenConduitAppliesOverNetlink checks the settings that keep the conduit
// from disturbing the CNI datapath, all of which must apply with CAP_NET_ADMIN
// alone and a read-only /proc/sys.
func TestHardenConduitAppliesOverNetlink(t *testing.T) {
	const parentName, conduitName = "tinkparent2", "tink-dhcp0"

	parent := mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	if err := addConduit(conduitName, parent.Attrs().Index, nsid); err != nil {
		t.Fatalf("addConduit() error = %v", err)
	}
	link, err := netlink.LinkByName(conduitName)
	if err != nil {
		t.Fatalf("LinkByName(%q) error = %v", conduitName, err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	if err := hardenConduit(logr.Discard(), link); err != nil {
		t.Fatalf("hardenConduit() error = %v", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatalf("bringing up the conduit: %v", err)
	}

	// Read back through /proc/sys, which is the same devconf the netlink
	// attributes address.
	for path, want := range map[string]string{
		"arp_ignore":       "8",
		"arp_announce":     "2",
		"accept_redirects": "0",
		"send_redirects":   "0",
	} {
		full := "/proc/sys/net/ipv4/conf/" + conduitName + "/" + path
		b, err := os.ReadFile(full)
		if err != nil {
			t.Errorf("reading %s: %v", full, err)
			continue
		}
		if got := strings.TrimSpace(string(b)); got != want {
			t.Errorf("%s = %q, want %q", full, got, want)
		}
	}

	fresh, err := netlink.LinkByName(conduitName)
	if err != nil {
		t.Fatalf("re-reading the conduit: %v", err)
	}
	if got := fresh.Attrs().MTU; got != conduitMTU {
		t.Errorf("conduit MTU = %d, want %d", got, conduitMTU)
	}

	// The MTU must have denied the link an IPv6 stack outright. If this directory
	// exists the link has an inet6_dev, and with it all-nodes multicast
	// membership and a router advertisement's route waiting to happen.
	if _, err := os.Stat("/proc/sys/net/ipv6/conf/" + conduitName); err == nil {
		t.Errorf("conduit has an inet6_dev at MTU %d; it should have none below IPV6_MIN_MTU", conduitMTU)
	}
	if addrs, err := netlink.AddrList(fresh, unix.AF_INET6); err == nil && len(addrs) != 0 {
		t.Errorf("conduit has %d IPv6 addresses, want none", len(addrs))
	}
}

// The conduit must carry no address of its own. It is a layer 2 conduit; an
// address on it would put the pod on the physical segment and give the kernel a
// route to install.
func TestConduitIsUnaddressed(t *testing.T) {
	const parentName, conduitName = "tinkparent3", "tink-dhcp0"

	parent := mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	if err := addConduit(conduitName, parent.Attrs().Index, nsid); err != nil {
		t.Fatalf("addConduit() error = %v", err)
	}
	link, err := netlink.LinkByName(conduitName)
	if err != nil {
		t.Fatalf("LinkByName(%q) error = %v", conduitName, err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	if err := hardenConduit(logr.Discard(), link); err != nil {
		t.Fatalf("hardenConduit() error = %v", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatalf("bringing up the conduit: %v", err)
	}

	for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
		addrs, err := netlink.AddrList(link, family)
		if err != nil {
			t.Fatalf("AddrList(family=%d): %v", family, err)
		}
		if len(addrs) != 0 {
			t.Errorf("conduit has addresses %v for family %d, want none", addrs, family)
		}
	}

	routes, err := netlink.RouteList(link, unix.AF_INET)
	if err != nil {
		t.Fatalf("RouteList: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("conduit has routes %v, want none", routes)
	}
}

// A conduit left behind by an ungraceful exit holds exactly the name the next
// start needs. Without the purge every later start fails with EEXIST and only
// deleting the pod recovers it.
func TestPurgeStaleConduits(t *testing.T) {
	const parentName = "tinkparent4"

	parent := mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	// One under the name about to be claimed, one under a name a different
	// configuration would have produced. Both must go: on acquiring leadership
	// this instance's conduit has to be the only one.
	stale := []string{"tink-dhcp0", "tink-dhcpabc123"}
	for _, name := range stale {
		if err := addConduit(name, parent.Attrs().Index, nsid); err != nil {
			t.Fatalf("addConduit(%q) error = %v", name, err)
		}
	}

	var logged strings.Builder
	log := funcr.New(func(prefix, args string) { logged.WriteString(prefix + args + "\n") }, funcr.Options{Verbosity: 1})

	purgeStaleConduits(log, "tink-dhcp0")

	for _, name := range stale {
		if _, err := netlink.LinkByName(name); err == nil {
			_ = netlink.LinkDel(mustLink(t, name))
			t.Errorf("conduit %q survived the purge", name)
		}
	}
	// The parent is not a conduit and must be left alone.
	if _, err := netlink.LinkByName(parentName); err != nil {
		t.Errorf("purge removed the parent interface %q: %v", parentName, err)
	}
	if !strings.Contains(logged.String(), "tink-dhcpabc123") {
		t.Errorf("purge did not report removing the unexpected name; logged:\n%s", logged.String())
	}
}

func mustLink(t *testing.T, name string) netlink.Link {
	t.Helper()
	l, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("LinkByName(%q): %v", name, err)
	}

	return l
}

// TestHostLinksListsAnotherNamespace exercises the RTM_GETLINK dump that carries
// IFLA_TARGET_NETNSID. It is the only rtnetlink dump that accepts a target
// namespace, and the conduit depends on it to find its parent without ever
// entering the host's namespace.
func TestHostLinksListsAnotherNamespace(t *testing.T) {
	const parentName = "tinkparent5"

	mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	links, err := hostLinks(nsid)
	if err != nil {
		t.Fatalf("hostLinks() error = %v", err)
	}

	var found *hostLink
	for i := range links {
		if links[i].name == parentName {
			found = &links[i]
		}
	}
	if found == nil {
		names := make([]string, 0, len(links))
		for _, l := range links {
			names = append(names, l.name)
		}
		t.Fatalf("hostLinks() did not return %q; saw %v", parentName, names)
	}
	if found.kind != "dummy" {
		t.Errorf("hostLinks() reported kind %q for a dummy interface, want %q", found.kind, "dummy")
	}
	if !found.up || !found.running {
		t.Errorf("hostLinks() reported %q as up=%t carrier=%t, want both true", parentName, found.up, found.running)
	}

	idx, err := hostLinkIndex(links, parentName)
	if err != nil {
		t.Fatalf("hostLinkIndex() error = %v", err)
	}
	if idx != found.index {
		t.Errorf("hostLinkIndex() = %d, want %d", idx, found.index)
	}

	if _, err := hostLinkIndex(links, "definitelynothere"); err == nil {
		t.Error("hostLinkIndex() found an interface that does not exist")
	}
}

// detectHostParent must never guess. A virtual interface is not a candidate, and
// an ambiguous or empty result has to be an error rather than an arbitrary pick,
// because choosing wrong attaches the DHCP server to the wrong network.
func TestDetectHostParentRefusesToGuess(t *testing.T) {
	nsid := netnsIDForSelf(t)

	// A dummy reports kind "dummy", so it is virtual and never a candidate. In a
	// namespace holding only virtual interfaces there is nothing to pick.
	mustCreateParent(t, "tinkparent6")

	links, err := hostLinks(nsid)
	if err != nil {
		t.Fatalf("hostLinks() error = %v", err)
	}

	got, err := detectHostParent(links)
	if err == nil {
		t.Fatalf("detectHostParent() = %q, want an error when no physical interface is present", got)
	}
	if !strings.Contains(err.Error(), "--dhcp-conduit-interface") {
		t.Errorf("detectHostParent() error does not tell the operator which flag to set: %v", err)
	}
}

// A parent already carrying a macvlan must produce a recognisable EBUSY. That is
// the error an operator with a Multus macvlan NetworkAttachmentDefinition on the
// same NIC will actually hit, and setupConduit matches on it to explain what
// happened; a change that stopped errors.Is matching would leave them with a
// bare "device or resource busy".
func TestAddConduitReportsMacvlanConflict(t *testing.T) {
	const parentName = "tinkbusy0"

	parent := mustCreateParent(t, parentName)
	nsid := netnsIDForSelf(t)

	if err := netlink.LinkAdd(&netlink.Macvlan{
		LinkAttrs: netlink.LinkAttrs{Name: "mvconflict", ParentIndex: parent.Attrs().Index},
		Mode:      netlink.MACVLAN_MODE_BRIDGE,
	}); err != nil {
		t.Skipf("cannot create the conflicting macvlan: %v", err)
	}
	t.Cleanup(func() {
		if l, err := netlink.LinkByName("mvconflict"); err == nil {
			_ = netlink.LinkDel(l)
		}
	})

	err := addConduit("tink-dhcp0", parent.Attrs().Index, nsid)
	if err == nil {
		t.Fatal("addConduit() succeeded on a parent that already carries a macvlan")
	}
	if !errors.Is(err, unix.EBUSY) {
		t.Fatalf("addConduit() error = %v (%T); errors.Is does not match unix.EBUSY, so the explanatory message would be lost", err, err)
	}
}

// TestSetupConduitEndToEnd runs the real setup path against a realistic
// topology: a parent NIC and a CNI-style veth in a separate "host" namespace,
// and this process in the "pod" namespace. Two namespaces cannot be built from
// inside a single Go test, so a driver has to make them, run this binary in the
// pod namespace and name the parent in TINK_E2E_PARENT. Without that the test
// skips; every other test in this file covers its piece on its own.
func TestSetupConduitEndToEnd(t *testing.T) {
	parent := os.Getenv("TINK_E2E_PARENT")
	if parent == "" {
		t.Skip("TINK_E2E_PARENT is not set; this test needs a host namespace prepared by its driver script")
	}

	t.Setenv(podUIDEnv, "550e8400-e29b-41d4-a716-446655440000")

	name, err := conduitIfaceName(false)
	if err != nil {
		t.Fatalf("conduitIfaceName() error = %v", err)
	}

	var logged strings.Builder
	log := funcr.New(func(prefix, args string) { logged.WriteString(prefix + args + "\n") }, funcr.Options{Verbosity: 1})

	cleanup, err := setupConduit(log, parent, name)
	if err != nil {
		t.Fatalf("setupConduit() error = %v\nlog:\n%s", err, logged.String())
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("conduit %q is not in the pod namespace: %v", name, err)
	}
	if link.Attrs().Flags&net.FlagUp == 0 {
		t.Errorf("conduit %q is not up", name)
	}
	if got := link.Attrs().MTU; got != conduitMTU {
		t.Errorf("conduit MTU = %d, want %d", got, conduitMTU)
	}
	if _, err := os.Stat("/proc/sys/net/ipv6/conf/" + name); err == nil {
		t.Errorf("conduit has an inet6_dev; the MTU should have denied it one")
	}
	for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
		if addrs, err := netlink.AddrList(link, family); err == nil && len(addrs) != 0 {
			t.Errorf("conduit has addresses %v for family %d, want none", addrs, family)
		}
	}

	// The pod's own routing must be exactly as the CNI left it.
	routes, err := netlink.RouteList(link, unix.AF_INET)
	if err != nil {
		t.Fatalf("RouteList: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("conduit installed routes %v, want none", routes)
	}

	cleanup()

	if _, err := netlink.LinkByName(name); err == nil {
		t.Errorf("conduit %q survived cleanup", name)
	}
}
