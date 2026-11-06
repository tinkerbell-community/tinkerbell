# Moving from v1alpha1 to v1alpha2

Tinkerbell stores its objects as `v1alpha2` and still serves `v1alpha1`, so existing clients
(Cluster API Provider Tinkerbell, the BMC discovery controller, manifests, scripts) keep working
while they move to `v1alpha2`. This document describes what happens on upgrade, how the two
versions map, and what does not carry over.

## What happens on upgrade

With `--enable-crd-migrations` (the Helm chart's default), Tinkerbell does the following at startup:

1. It loads the conversion webhook's CA and serving certificate from a Secret
   (`--conversion-webhook-cert-secret`), creating them on first start. The serving
   certificate is re-signed by the same CA before it expires, so the CA bundle in the CRDs
   stays valid.
2. It starts the conversion webhook (`--conversion-webhook-bind-addr`, default `:9443`).
3. It installs or updates the CRDs. Hardware, Workflow and bmc Job serve both versions and
   store `v1alpha2`; the API server reaches the webhook through the
   `--conversion-webhook-service-*` Service, or `--conversion-webhook-url` when it cannot
   reach Services.
4. It rewrites every Hardware, Workflow and Job so it is stored as `v1alpha2`, then sets the
   CRDs' `status.storedVersions` to `v1alpha2`. Reading the objects stored as `v1alpha1` needs
   the webhook, so the pod becomes ready only after this step, and the chart's webhook Service
   publishes the pod before it is ready (`publishNotReadyAddresses`). Until the API server
   reaches the webhook, Tinkerbell retries the rewrite for up to five minutes. Releases before
   v0.28.2 lacked both, so their pod could not finish this step behind the Service and
   restarted until it was replaced: upgrade from v0.26 or earlier straight to v0.28.2 or later.
   A release that is stuck this way is fixed by upgrading it to v0.28.2 or later. Do not roll
   back to v0.26: it cannot read objects whose CRDs already point at the webhook.
5. It translates the kinds that only exist in `v1alpha1` (see below) and keeps translating
   them while they exist: the Tink controller translates Templates and rule sets, Rufio
   translates bmc Machines.
6. With `--enable-deprecated-crd-removal`, it removes the CRDs of those kinds once their
   objects have moved (see [Removing the v1alpha1-only CRDs](#removing-the-v1alpha1-only-crds)).

Running it again does nothing. Without `--enable-crd-migrations` the webhook still runs, for
CRDs managed elsewhere: point their `spec.conversion` at it. Tinkerbell reads `v1alpha2`, so
CRDs managed elsewhere must be updated to these versions (`crd/bases`) before Tinkerbell is
upgraded; with the old `v1alpha1`-only CRDs it does not start.

All `v1alpha1` versions are deprecated; clients get a warning naming the replacement.

## Templated Hardware

With `--backend-kube-rendering-enabled`, Tinkerbell renders templates in a Hardware's spec
before serving it. Hardware created through `v1alpha1` (it has the `conversion.tinkerbell.org/v1alpha1`
annotation) is rendered as before, against its `v1alpha1` form, so existing templates keep
working. Other Hardware is rendered in its `v1alpha2` form: templates see `.hardware` (the
Hardware) and `.references`, and the paths in the `tinkerbell.org/render-skip` annotation are
`v1alpha2` paths, with `*` matching any key of a map (for example
`spec.networkInterfaces.*.ipam.ipv4.hostname`).

## Kinds

| v1alpha1                       | v1alpha2                          | How                                      |
| ------------------------------ | --------------------------------- | ---------------------------------------- |
| `Hardware`                     | `Hardware`                        | Same object, converted by the webhook    |
| `Workflow`                     | `Workflow`                        | Same object, converted by the webhook    |
| `bmc.tinkerbell.org` `Job`     | `bmc.tinkerbell.org` `Job`        | Same object, converted by the webhook    |
| `Template`                     | `Task`                            | Translated, when possible (see below)    |
| `WorkflowRuleSet`              | `Policy` (`workflowAutoCreation`) | Translated                               |
| `bmc.tinkerbell.org` `Machine` | `Hardware.spec.bmc`               | Translated into the Hardware that refers to it |
| `bmc.tinkerbell.org` `Task`    | `Job.spec.operations`             | Still run as before; not translated      |

## How the versions map

Each conversion records, in an annotation on the object it produces, what the target version
cannot represent (`conversion.tinkerbell.org/v1alpha1` on `v1alpha2` objects,
`conversion.tinkerbell.org/v1alpha2` on `v1alpha1` objects). An object that is not changed in
between converts back exactly as it was. When it is changed, the change wins and the data only
the other version can hold is kept. Do not edit these annotations.

### Hardware

| v1alpha1                                           | v1alpha2                                                           |
| -------------------------------------------------- | ------------------------------------------------------------------ |
| `interfaces[]` (keyed by `dhcp.mac`)               | `networkInterfaces` (map keyed by MAC, lower case)                 |
| `dhcp.ip` `address`, `netmask`, `gateway`          | `ipam.ipv4` (or `ipam.ipv6`) `address`, `prefix`, `gateway`         |
| `dhcp.hostname`, `vlan_id`, `name_servers`, `time_servers`, `classless_static_routes` | `ipam.ipv4` `hostname`, `vlanID`, `nameservers`, `ntpServers`, `classlessStaticRoutes` |
| `dhcp.domain_name`                                 | first entry of `ipam.ipv4.domainSearchList`                        |
| `dhcp.lease_time`                                  | `dhcp.ipv4.leaseTimeSeconds` (IPv6: `dhcp.ipv6.validLifetime`)     |
| `dhcp.tftp_server_name`, `boot_file_name`          | `netboot.ipv4` `tftpServerName`, `bootFileName`                    |
| `dhcp.arch`                                        | `spec.arch`                                                        |
| `disableDhcp`                                      | `dhcp.ipv4.disabled` and `dhcp.ipv6.disabled`                      |
| `netboot.allowPXE`                                 | `netboot.disabled` (inverted; an unset `allowPXE` means disabled)  |
| `netboot.ipxe` `url`, `contents`, `binary`         | `netboot.ipxe` `url`, `script`, `binary`                           |
| `netboot.pxelinux`, `netboot.rpi`                  | `netboot.pxelinux`, `netboot.rpi`                                  |
| `netboot.osie` `baseURL` + `kernel`/`initrd`       | `instance.osie` `kernelURL`/`initrdURL` (see below)                |
| `netboot.osie.kernelParams`                        | `instance.osie.kernelParams`                                       |
| `isoboot.sourceISO`                                | `instance.osie.isoURL`                                             |
| `metadata.instance.id`                             | `instance.lookupID`                                                |
| `metadata.instance.ssh_keys`                       | `instance.sshKeys`                                                 |
| `metadata.osieFiles`                               | `instance.osie.files`                                              |
| `userData`, `vendorData`                           | `instance.userdata`, `instance.vendordata`                         |
| `disks[].device`                                   | `storageDevices[].name`                                            |
| `references`, `agentID`, `auto`                    | `references`, `agentID`, `auto`                                    |
| `bmcRef` (a bmc `Machine`)                         | `bmc.connection`, filled in from the Machine by translation        |
| `status.attributes`                                | `status.attributes`                                                |

v1alpha1 configures the OSIE per interface, v1alpha2 per Hardware: the first interface that sets
it wins. A v1alpha1 `baseURL` with no `kernel` becomes `<baseURL>/vmlinuz-${arch}` (likewise
`initramfs-${arch}`), the names Smee uses by default; iPXE expands `${arch}`. A `kernel` with no
`baseURL` becomes a relative URL, resolved against Smee's default OSIE URL.

Only in v1alpha1, kept in the annotation: interfaces without a MAC, `dhcp.iface_name`,
`dhcp.uefi`, `netboot.allowWorkflow`, every other `metadata` field (facility, plan, operating
system, storage, ...), `tinkVersion`, `resources`, `status.state`. Smee used
`metadata.facility.facility_code` for the `facility` kernel argument; v1alpha2 has no facility.

Only in v1alpha2: a second address family per interface, DHCPv6 lifetimes, `netboot.ipv6`,
extra search domains, `bmc`, `instance` `metadata`, `networkConfig` and `vars`, `status.conditions`.

### Workflow

A v1alpha1 Workflow runs a Template; a v1alpha2 Workflow runs a list of Tasks. A Workflow
created through v1alpha1 becomes a v1alpha2 Workflow with:

- the annotation `tinkerbell.org/v1alpha1-template: <template name>`,
- `globals.templateMap` set to the v1alpha1 `hardwareMap`,
- one `tasks` entry holding `hardware.hardwareRef` and `hardware.bootOptions` and no `taskRef`.

Unless the engine rewrites it onto Tasks (see [Workflows onto Tasks](#translated-kinds)), such a
Workflow runs in **v1alpha1 compatibility mode**: the Template is rendered with the
v1alpha1 template data (`.Hardware`, `.hardware` as the v1alpha1 Hardware, the `hardwareMap`
keys, `.references`, `.metadata_url`), exactly as before. Its status is written in v1alpha2 form
and reads back as v1alpha1 status.

Boot options map field for field (`toggleAllowNetboot` is `toggleNetboot`; boot mode `iso` is
`isoboot`). The status maps state by state: `metadata.workflow.state` is the v1alpha1 `state`,
`metadata.task` and `metadata.action` are `currentState`, `renderedTasks` are `tasks`.
A v1alpha1 action `command` becomes `command` (its first element) and `args` (the rest). The
Tink Agent receives them as one list and passes it to the container as its command, as before;
the v1alpha2 meaning of `command` (overriding the image's entrypoint) needs an Agent protocol
change and is not implemented.

### bmc Job

`tasks` become `operations`. A v1alpha2 operation does one thing, so a v1alpha1 action that sets
several fields becomes several operations, in the order Rufio applies them; the deprecated
`oneTimeBootDeviceAction` becomes a non-persistent `bootDevice`. `machineRef` is kept in the
annotation `bmc.tinkerbell.org/machine: <namespace>/<name>`; Rufio reads the BMC connection
from that Machine for Jobs that have no `connection` or `hardwareRef`.

## Translated kinds

**Template to Task.** A Template's `data` is Go-templated YAML, so it can only be turned into
Tasks when its structure does not depend on the template: every `{{ }}` sits inside a string
value, with no `range`, `if`, `with`, `define`, `template` or `block` around tasks or actions,
and every variable it uses has a v1alpha2 equivalent. Such a Template becomes one Task per
template task, named `<template>-<task>`, owned by the Template. Otherwise the Template is
annotated `tinkerbell.org/v1alpha2-translation: <reason>` and an event says why; Workflows that
use it keep running in v1alpha1 compatibility mode.

**Workflows onto Tasks.** When the Workflow engine first sees a Workflow created through v1alpha1
whose Template converts, it rewrites the Workflow to run the Template's Tasks before starting it:
one `tasks` entry per template task, on the Agent its `worker` names (the `hardwareMap` value),
bound to the Workflow's Hardware, with `global_timeout` as `timeoutSeconds`. The rendered tasks
keep the template task names. A v1alpha1 client still reads the Workflow it created, with the same
spec and the same state. Workflows that already started are not rewritten and run in
compatibility mode; that includes every Workflow an earlier Tinkerbell made `PENDING`, which was
offered to its Agent then and is not changed under it. These are also not rewritten and run in
compatibility mode, with an event saying why: Workflows with boot options whose Template has
several tasks (v1alpha2 applies boot options per task, so netboot would be turned off, and post
operations run, after the first task instead of after the last); Workflows whose `hardwareMap`
lacks a task's worker key or sets `metadata_url` (inside a Task it is always Tinkerbell's); and
Workflows whose Tasks would not render for their Hardware.

**WorkflowRuleSet to Policy.** A rule set whose Template converts, and whose tasks all run on
the enrolling Agent (each task's worker is the rule set's `agentValue`), becomes a Policy of the
same name, owned by it, with one `workflowAutoCreation` rule running the Template's Tasks. The
rule's Workflows are created in the Policy's namespace, as v1alpha1 rule sets now do. A rule set
with a Policy enrolls Agents only through that Policy, so no Workflow is created twice. Other
rule sets are annotated `tinkerbell.org/v1alpha2-translation: <reason>` and keep enrolling as
before, creating Workflows that run in compatibility mode.

**bmc Machine to Hardware.** A Hardware whose `bmcRef` names a Machine gets
`spec.bmc.connection` from it (host, port, credentials, provider options), unless it already
has one, and the annotation `bmc.tinkerbell.org/machine: <namespace>/<name>`. While the
annotation is there, the connection follows the Machine and the Hardware's `status.conditions`
(`Contactable`, `PowerState`) show the Machine's state: Rufio keeps polling the Machine and does
not poll the BMC a second time. A connection without the annotation is never changed; remove the
annotation to manage the connection on the Hardware. Hardware with its own connection is polled
by Rufio directly, which also collects its out-of-band inventory.

**bmc Jobs no longer create Tasks.** Rufio runs a Job's operations itself, in order, and records
progress in `status.completedOperations`; the conditions are the same as before (`Running`, then
`Completed` or `Failed`). A Job started by an older Rufio before the upgrade is taken over: its
completed Tasks count as done. Rufio runs without leader election, so do not let an old and a new
Tinkerbell run side by side during the upgrade: both would act on new Jobs. The chart defaults to
a rolling update; use `deployment.strategy.type: Recreate` for the upgrade. A release installed
by chart v0.28.1 or earlier cannot be switched to `Recreate` by Helm alone: the Deployment keeps
the `rollingUpdate` settings the API server defaulted, which `Recreate` rejects. Switch it once
before the upgrade:

```sh
kubectl -n <namespace> patch deployment <release> --type=merge \
  -p '{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}'
helm upgrade <release> ... --set deployment.strategy.type=Recreate
```

From chart v0.28.2 on, the chart sets `rollingUpdate` itself, so Helm switches between the two.

## Removing the v1alpha1-only CRDs

With `--enable-deprecated-crd-removal` (chart value `enableDeprecatedCRDRemoval`, off by default)
and `--enable-crd-migrations`, the startup migration also removes the CRDs of the kinds that only
exist in `v1alpha1`, once their objects have moved to `v1alpha2`. Deleting a CRD deletes all its
objects, so a CRD is only removed when nothing is lost or still needs it:

| CRD                               | Kept while                                                                                         |
| --------------------------------- | -------------------------------------------------------------------------------------------------- |
| `workflowrulesets.tinkerbell.org` | a rule set has no `Policy` translated from it                                                       |
| `templates.tinkerbell.org`        | the `WorkflowRuleSet` CRD exists; a Template has no `Task` translation (or not all its Tasks yet); a Workflow that has not started runs a Template in v1alpha1 compatibility mode |
| `machines.bmc.tinkerbell.org`     | a Machine is not translated into any Hardware; a Hardware refers to a Machine and has no connection yet; a Job that has not finished reads its connection from a Machine |
| `tasks.bmc.tinkerbell.org`        | a Task has not finished; a Job that has not finished continues from the Tasks an older Rufio created |

A CRD that is kept is logged with the reasons (`keeping deprecated CRD`), and checked again the
next time Tinkerbell starts: the Tink controller and Rufio keep translating while it runs. Delete
or fix the objects named there to let it go.

Before deleting a CRD, Tinkerbell detaches what moved from it: it removes owner references to its
kind from the other Tinkerbell objects, so the garbage collector does not delete the Tasks of a
Template, the Policy of a rule set, or the Workflows a rule set created; and it removes the
`bmc.tinkerbell.org/machine` annotation from Hardware, whose translated connection becomes their
own, polled by Rufio directly. A removed CRD is not installed again. With the CRDs gone, the
controllers of those kinds do not run, Workflows still created through v1alpha1 fail with
`template not found`, and Hardware and Jobs need a BMC connection of their own.

Owner references in objects that are not Tinkerbell's are not touched. Move every client that
still creates or reads these kinds to `v1alpha2` before turning this on, and stop it, as objects it
creates between the check and the deletion are deleted with the CRD:

- Cluster API Provider Tinkerbell v0.9.1 and earlier can neither provision nor delete machines
  once the Template CRD is gone: they look for the machine's Template first and only treat "not
  found" as no Template. Even without that, CAPT needs a Template for a template that does not
  translate (it runs it in compatibility mode) and for a `templateRef`; before v0.9 it creates a
  Template for every machine.
- The BMC discovery controller creates bmc Machines.

## Behavior that changes

Hardware created through `v1alpha1` is served as before, except:

- An interface without a `netboot` block got no DHCP reply at all; it now gets DHCP without
  netboot options.
- An interface with an IP address but no MAC cannot be found by its IP any more (v1alpha2
  identifies interfaces by MAC; the interface only survives in the conversion annotation).
- A non-contiguous `v1alpha1` netmask has no prefix form and is now an error.
- When interfaces set different OSIE settings, the first interface's apply to all of them.
- An OSIE `baseURL` with no path no longer takes the default OSIE URL's path, and Smee's
  configured default kernel and initrd names no longer apply to Hardware that names its OSIE
  base URL. Set `instance.osie.kernelURL`/`initrdURL` explicitly if you relied on either.

- An interface with no `lease_time` (or zero) was served DHCPv4 lease time 0; it now gets the
  `v1alpha2` default, 86400 seconds (DHCPv6 keeps Smee's default lifetime). An interface that sets a
  `lease_time` is still served it over both DHCPv4 and DHCPv6.

## Workflow engine notes

- When a task's last action succeeds, the Workflow moves to the next task's Agent right away, so a
  v1alpha1 client sees `currentState.agentID` equal to `status.agentID` (only `status.state` was
  ever needed to follow a Workflow).
- A deprecated v1alpha1 action `pid` reads back as `namespaces.pid`; the Agent gets the same value.
- Netboot toggling skips interfaces without a MAC.
- Choices for native v1alpha2 Workflows, which upstream does not specify yet: a Workflow may only
  use Hardware in its own namespace; a task without an `agentID` runs on its Hardware's Agent;
  environment variables layer Task, Workflow `globals`, then the task's `extra`; a task whose
  actions are all skipped (`if`) counts as done; a zero timeout means none. A Workflow's
  `.hardware` is the first task's Hardware.
- Workflow, task and action states are stored by name in v1alpha2 (`Preparing`, `Pending`,
  `Running`, `Post`, `Success`, `Failed`, `Timeout`), as `kubectl get workflows` shows them;
  v1alpha1 clients still read `PENDING`, `RUNNING`, .... Tinkerbell v0.27 stored the state as a
  number; such Workflows still read correctly and get the name on their next status update.

Tootles also serves the cloud-init NoCloud datasource now (`/nocloud/...`), from the Hardware's
`instance` `metadata`, `userdata`, `vendordata` and `networkConfig`; see
[Ports and endpoints](../PORTS_AND_ENDPOINTS.md).

## Changes to v1alpha2 made here

Compared to upstream v1alpha2, this fork adds what v1alpha1 gained after v1alpha2 was added
(status inventory attributes, OSIE files, `networkBootConfig`, the HomeAssistant BMC provider,
the BMC connection port, Agent attribute names, the Policy namespace rule) and two fields the
conversion needs: Workflow status `metadata.message`, and an Action `command` that may be a
program name looked up in the image's `PATH`, not only an absolute path.
