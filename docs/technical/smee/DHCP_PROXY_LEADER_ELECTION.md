# DHCP leader election

Two DHCP servers answering the same broadcast domain race each other, and a PXE client will act on whichever reply reaches it first. When Smee runs with more than one replica, leader election makes sure only one of them answers.

DHCP is the only leader-elected service in the binary. HTTP, TFTP, syslog, the Tink server and the controllers run on every replica regardless.

## Configuration

```bash
--dhcp-enable-leader-election=true     # TINKERBELL_DHCP_ENABLE_LEADER_ELECTION
--dhcp-leader-election-namespace=tink  # TINKERBELL_DHCP_LEADER_ELECTION_NAMESPACE
```

In the Helm chart these are `deployment.envs.dhcp.enableLeaderElection` (default `true`) and `deployment.envs.dhcp.leaderElectionNamespace`.

Leader election requires the kube backend and is skipped for the file and none backends, where it would have no API server to hold a Lease in. When the namespace is not set, the pod's own namespace is read from `/var/run/secrets/kubernetes.io/serviceaccount/namespace`.

## What it does

Instances campaign for a `coordination.k8s.io` Lease named `smee-dhcp.tinkerbell.org`. The holder binds the DHCP socket and serves; everyone else waits.

| Setting | Value |
|---|---|
| Lease name | `smee-dhcp.tinkerbell.org` |
| Lease duration | 15s |
| Renew deadline | 10s |
| Retry period | 2s |
| Identity | `<hostname>_<uuid>` |
| Release on cancel | yes |

The identity carries a UUID as well as the hostname so that a restarted process is not mistaken for the previous one on the same pod, which may not have fully exited.

The timings match client-go's defaults but are stated explicitly in the code because DHCP correctness depends on the relationship between them: a challenger waits a full lease duration past the last renewal it saw before taking over, while the current holder stops serving at the renew deadline. The gap is what keeps the two from ever answering at once.

### Losing the Lease is not an error

An instance that loses the Lease stops the DHCP server, closes its socket and returns to standby, then campaigns again. It does not exit.

Treating it as fatal would take every other service in the binary down with it, and a Lease that flapped would put the whole process into `CrashLoopBackOff`. Only a genuine DHCP failure — failing to bind, say — stops the process.

### Stopping at the renew deadline rather than on cancellation

client-go cancels the leader context only after its renew loop has given up *and* its release call has returned. That release is a blocking API call, so in the very situation where the Lease is being lost — the API server being unreachable — it can run until the client timeout, carrying the instance well past the point where a challenger takes over.

Smee therefore polls `LeaderElector.Check` with a negative tolerance, which brings the cutoff forward from the lease duration to the renew deadline, and stops serving there.

The socket must also be closed before campaigning again. The DHCP listener sets `SO_REUSEPORT`, so a second bind would otherwise quietly succeed alongside the first and the instance would answer twice. `leadDHCP` waits for `serveDHCP` to return before re-entering the campaign.

## Interaction with the DHCP conduit

When `--dhcp-conduit-enabled` is set, the conduit interface's lifetime follows the DHCP server's: it is created when the Lease is acquired and removed when it is lost, so an instance standing by holds no interface on the physical network at all. It is named `tink-dhcp0` — a fixed name, because under leader election there is only ever meant to be one.

On acquiring the Lease, every interface matching the conduit naming pattern is removed before the new one is created. That clears a conduit left behind by an ungraceful exit: a pod's network namespace outlives its container, so a leftover would otherwise hold exactly the name the next start needs and every restart would fail with `EEXIST`.

See [DHCP boot modes](../DHCP_BOOT_MODES.md) for the conduit itself.

## RBAC

The chart creates a namespaced Role granting the leader election permissions:

```yaml
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
```

## Observing it

```bash
# Who currently holds the Lease
kubectl get lease smee-dhcp.tinkerbell.org -n tink -o yaml

# Acquisition and loss are logged by the "leaderelection" logger
kubectl logs -n tink deploy/tinkerbell | grep -i leadership
```

Expect `acquired dhcp leadership, starting dhcp server` on the holder and nothing on the others until a handover.
