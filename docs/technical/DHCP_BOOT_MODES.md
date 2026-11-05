# DHCP Boot Modes

This document explains the different DHCP boot modes available in Tinkerbell and how to configure them.

## Overview

As requirements across users can vary, flexibility in DHCP boot modes is essential for accommodating different network environments and client needs.
Tinkerbell provides several DHCP boot modes to cater to these requirements. The modes include DHCP Reservation, Proxy DHCP, Auto Proxy DHCP, and DHCP disabled. Each mode has its own use case and configuration options.

## DHCP Modes

### DHCP Reservation

This mode is used to provide IP addresses and next boot information to clients based on their MAC addresses. In this mode, the IP address is reservation-based, meaning there must be a corresponding Hardware object for the requesting client's MAC address.

#### DHCP Reservation Configuration

This is the default mode. To explicitly enable this mode use the CLI flag `--dhcp-mode=reservation` or the environment variable `TINKERBELL_DHCP_MODE=reservation`.

### Proxy DHCP

This mode is used to provide next boot information to clients. In this mode, a Hardware object must exist for the requesting client's MAC address. In this mode Tinkerbell does NOT provide IP addresses to clients, it only provides next boot information. A DHCP server on the network must be configured to provide IP addresses to clients. Tinkerbell requires Layer 2 access to machines or a DHCP relay agent that will forward DHCP requests to Tinkerbell.

#### Proxy DHCP Configuration

To enable this mode set the CLI flag `--dhcp-mode=proxy` or the environment variable `TINKERBELL_DHCP_MODE=proxy`.

### Auto Proxy DHCP

This mode is used to provide next boot information to clients without requiring a pre-existing Hardware object. In this mode, Tinkerbell will respond to PXE enabled DHCP requests from clients and provide them with next boot info when network booting. All network booting clients will be provided the next boot info for the iPXE binary. When a client needs an iPXE script, if no corresponding Hardware object is found for the requesting client's MAC address, Tinkerbell will provide the client with a statically defined iPXE script. If a Hardware record is found, then the normal `auto.ipxe` script will be served. In this mode Tinkerbell does NOT provide IP addresses to clients, it only provides next boot information. A DHCP server on the network must be configured to provide IP addresses to clients. Tinkerbell requires Layer 2 access to machines or a DHCP relay agent that will forward DHCP requests to Tinkerbell.

#### Auto Proxy DHCP Configuration

To enable this mode set the CLI flag `--dhcp-mode=auto-proxy` or the environment variable `TINKERBELL_DHCP_MODE=auto-proxy`.

### DHCP Disabled

This mode is used to disable all DHCP functionality in Tinkerbell. In this mode, the user is required to handle all DHCP functionality.

#### DHCP Disabled Configuration

To enable this mode set the CLI flag `--dhcp-enabled=false` or the environment variable `TINKERBELL_DHCP_ENABLED=false`.

## Reaching the DHCP broadcast domain

A DHCP client that has no address yet broadcasts to 255.255.255.255, and a limited broadcast is never forwarded by a router. Smee therefore has to be on the same layer 2 segment as the machines it boots. There are three ways to arrange that.

### DHCP relay (no special privileges)

Point an `ip helper-address` on the provisioning VLAN at Smee. The relay rewrites each request as routable unicast with `giaddr` set, and Smee answers it through the ordinary pod network. Nothing below needs to be enabled, the pod needs no added capabilities, and the CNI datapath is used throughout. Prefer this where the network equipment allows it.

### DHCP conduit

Where no relay is available, Smee can give itself an interface directly on the physical segment: an ipvlan (layer 2 mode) or a macvlan (bridge mode), attached to one of the host's interfaces and created inside the pod's own network namespace.

```bash
--dhcp-conduit-enabled=true
--dhcp-conduit-type=ipvlan      # optional; ipvlan (default) or macvlan, see below
--dhcp-conduit-interface=eth0   # optional; auto-detected when the host has one physical NIC
```

Or equivalently:

```bash
TINKERBELL_DHCP_CONDUIT_ENABLED=true
TINKERBELL_DHCP_CONDUIT_TYPE=ipvlan
TINKERBELL_DHCP_CONDUIT_INTERFACE=eth0
```

Both types are created, hardened and removed the same way, and everything below applies to either; the type only decides how the conduit shares its parent interface.

The interface holds no IP address and installs no route, so the pod's routing table stays exactly as the CNI left it. Its MTU is set below `IPV6_MIN_MTU`, which makes the kernel decline to give it an IPv6 stack at all — no link-local address, no all-nodes multicast membership, and so nothing that could act on a router advertisement and install an IPv6 default route around the CNI. ARP is switched off for it with `arp_ignore=8`, so the pod never answers for its cluster address on the physical segment.

Requirements and constraints:

- `CAP_NET_ADMIN`, and nothing else. Not a privileged container, not `CAP_SYS_ADMIN`, not `hostPID`. The Helm chart adds the capability automatically when `deployment.envs.dhcp.conduitEnabled` is true.
- The pod must be on a CNI that hands it one end of a veth pair (Cilium, Calico and flannel all do). That is what records the host's network namespace against the pod's interface, which is how the conduit names the host's namespace without entering it. It does not work with `hostNetwork: true` — but then the host's interfaces are already available directly.
- **The parent interface must not already carry the other type.** A netdev has a single `rx_handler` slot, so an ipvlan and a macvlan cannot share a parent; the second one fails with `EBUSY`, and Smee says which type would fit. Bridge, bond and Open vSwitch ports hold the slot too, and take neither.

#### Choosing the conduit type

| | `ipvlan` (default) | `macvlan` |
|---|---|---|
| MAC address | The parent's | Its own, randomly generated each time the conduit is created |
| Parent NIC | Gains no unicast address, so is never pushed into promiscuous mode | Gains one; a NIC without unicast filtering only receives it in promiscuous mode |
| Shares a parent with | Other ipvlans | Other macvlans, e.g. a Multus macvlan `NetworkAttachmentDefinition` |

Use `ipvlan` unless the parent already carries a macvlan. The Cadence GEM (`macb`) behind the Raspberry Pi 5's RP1 is a NIC that cannot filter on more than one unicast address, and going promiscuous also turns off its hardware RX checksum offload — the reason ipvlan is the default.

Use `macvlan` when the NIC already hosts a macvlan, typically a Multus attachment used by other workloads: an ipvlan conduit cannot attach there at all, while a macvlan conduit joins the existing ones without displacing them. In bridge mode it also hears DHCP from clients attached through another macvlan on the same host, such as a VM.

### Interface naming and leader election

Leader election gates DHCP serving so that only one instance answers at a time:

```bash
--dhcp-enable-leader-election=true          # TINKERBELL_DHCP_ENABLE_LEADER_ELECTION
--dhcp-leader-election-namespace=tink       # TINKERBELL_DHCP_LEADER_ELECTION_NAMESPACE
```

It requires the kube backend and is ignored for the file and none backends. Losing the Lease stops the DHCP server and returns that instance to standby; it is not treated as fatal, because DHCP is the only leader-elected service in the binary.

The conduit's lifetime follows the DHCP server's, and so its name follows the leader-election setting:

| Leader election | Interface name | Lifetime |
|---|---|---|
| Enabled | `tink-dhcp0` | Created when the Lease is acquired, removed when it is lost. An instance standing by holds no interface on the physical network. |
| Disabled | `tink-dhcp<6 hex of pod UID>` | Created at startup, removed at shutdown. The per-pod suffix keeps replicas on one node from colliding. |

Under leader election the conduit is a singleton, so every interface matching either pattern is removed before the new one is created. That also clears a conduit left behind by an ungraceful exit: the pod's network namespace outlives the container, so without the sweep a leftover would hold the name the next start needs and every restart would fail with `EEXIST`.

### Binding to an existing interface

If an interface on the segment is already present in the pod — placed there by Multus, for example — skip the conduit and name it directly:

```bash
--dhcp-bind-interface=net1
```

## Interoperability with other DHCP servers

When a DHCP server exists on the network, Tinkerbell should be set to run `proxy` or `auto-proxy` mode. This will allow Tinkerbell to provide the next boot information to clients that request it and the existing DHCP server will provide IP address information. Layer 2 access to machines or a DHCP relay agent that will forward the DHCP requests to Tinkerbell is required.
