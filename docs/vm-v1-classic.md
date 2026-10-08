# Classic TrueNAS vm-v1 compatibility profile

Authority: `SemperSupra/garm-provider-truenas-private#15`, rolled up by
`SemperSupra/garm-provider-truenas-private#43`.

This is a source/static compatibility profile, not a runtime support claim.
25.04.1 remains deliberately `NOT_ADMITTED`; the initial vm-v1 family covers
25.04.2.6, 25.10.7 and 26.0.0-BETA.3 only.

## Driver boundary

The provider uses supported TrueNAS `vm.*` and `vm.device.*` middleware.
It must not call libvirt/qemu directly. VM lifecycle differences remain inside
this backend; they are not normalized into the Apps or Container drivers.

All admitted rows require:

- `system.version`
- `vm.query`
- `vm.create`
- `vm.update`
- `vm.clone`
- `vm.delete`
- `vm.start`
- `vm.stop`
- `vm.poweroff`
- `vm.status`
- `vm.device.query`
- `vm.device.create`
- `vm.device.delete`

The machine-readable exact source identities are in
`internal/truenasstore/testdata/runtime-backend-target-matrix.json`.

## Initial profile

`truenas-vm-linux-general` is Linux amd64 only and starts conservatively:

- 4 vCPUs;
- 8 GiB memory;
- no CPU pinning;
- no memory overcommit;
- `autostart=false`;
- no PCI/USB/GPU passthrough;
- no nested-virtualization claim;
- no Docker/container-action claim.

Unlike the system-container API, the classic VM API exposes enforceable vCPU
and memory configuration. Those resource claims still require exact runtime
read-back before admission.

## Image and disk strategy

The first implementation should use an exact, Foundry-qualified **template VM**
with a ZVOL-backed boot `DISK`, then call `vm.clone` per GARM runner.

This is preferable to rebuilding a guest disk for every job because the exact
upstream `vm.clone` implementation:

- creates a new VM identity;
- snapshots and clones ZVOL-backed `DISK` devices;
- regenerates NIC MAC identity;
- copies relevant VM firmware state;
- has rollback for clone/snapshot failures.

The source explicitly skips `RAW` disks during clone, so a RAW-backed template
must fail qualification rather than silently producing a diskless runner.

The immutable template itself is deployment/Foundry state, not provider-owned
ephemeral capacity. Its classic TrueNAS VM name is fixed as
`garm_tpl_ubuntu_2404_20260926_amd64`; classic `vm.*` accepts only letters,
digits, and underscores, so provider-owned runner names use the same restricted
alphabet and fail source/static validation if that invariant changes. The initial
source is pinned to Canonical's dated Ubuntu
24.04 release image `20260926`:
`ubuntu-24.04-server-cloudimg-amd64.img`, SHA-256
`6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2`.
A runtime runner clone is provider-owned and must be distinguishable from the
template and from foreign VMs. Floating `current` image identity is not an
admission oracle.

## Bootstrap channel

Cloning solves the boot-disk problem but not per-runner bootstrap identity. A
generic template must receive the ephemeral GARM bootstrap token and callback
metadata without baking them into the template.

The bootstrap channel is therefore an explicit unresolved gate, not something
the provider may improvise at runtime. Candidate mechanisms must be tested
against these invariants:

1. the immutable template contains no reusable GARM or GitHub credential;
2. each clone receives only its own ephemeral bootstrap material;
3. token/JIT material is excluded from ownership metadata and sanitized
   receipts;
4. persisted bootstrap material is removed or rendered unusable after the
   first successful boot;
5. a stopped/retired one-job VM is never restarted with stale credentials;
6. the mechanism is supported through TrueNAS middleware plus normal guest
   interfaces, not direct libvirt/qemu mutation.

The selected candidate is a per-instance NoCloud seed device, but it does not
depend on a preinstalled GARM binary. The seed carries the non-secret runner
bootstrap program and a small root launcher, creates a dedicated
`garm-runner` system user, and places only the per-instance callback/metadata
values and bootstrap token in a root-only one-shot environment file. The root
launcher parses an exact key allowlist without `eval`, deletes that file, then
drops to `garm-runner` before executing the shared runner bootstrap. This
keeps the stock Ubuntu template credential-free and removes an otherwise
undocumented baked-file dependency.

The seed remains attached while the VM is active because supported TrueNAS
`vm.device.delete` forbids live removal. The one-shot environment file,
however, must be deleted before the runner process starts. Immediately after
that deletion the launcher emits the deterministic
`GARM_VM_BOOTSTRAP_CONSUMED_V1:<provider-vm-name>` marker to the guest console.
The TrueNAS console is therefore the independent pre-B4 consumption oracle; it
does not imply callback/JIT success. Seed CDROM and dataset retirement still
occurs only after the VM is stopped.

## Ownership and lifecycle

Provider ownership must be recoverable from VM name/description plus expected
profile/template lineage without depending on secrets.

Initial lifecycle:

1. discover exact TrueNAS version and vm-v1 row;
2. observe the exact immutable template and reject drift;
3. clone to deterministic provider-owned runner identity;
4. apply/read back CPU, memory, autostart, ownership and device invariants;
5. materialize the bounded bootstrap channel;
6. start exactly once and prove guest/runner readiness;
7. on retirement, stop/power off if necessary;
8. delete with `zvols=true`;
9. independently prove VM absence and zero provider-owned cloned ZVOL/bootstrap
   residue.

Unknown ownership, missing template lineage, unexpected devices, active foreign
state, or ambiguous cleanup fails closed.

## Qualification ladder

- B0 exact source/API/template/profile fingerprint.
- B1 deterministic clone + ownership desired state.
- B2 clone/create and independent VM/device read-back.
- B3 guest bootstrap/start, Get/List, adoption/reconnect.
- B4 bounded synthetic GitHub/JIT boundary in public nested TrueNAS.
- B5 retirement/reconciliation plus VM/ZVOL/bootstrap zero residue.
- B6 one real capacity-one GitHub job on the selected physical target.
- B7 separate stronger gates for Docker, Windows, GPU and concurrency.

Runtime support never inherits between exact TrueNAS rows. The shared vm-v1
shape only permits code reuse; each admitted version still requires its own
B0-B5 receipt.
