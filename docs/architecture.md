# GARM Provider for TrueNAS

This provider implements the GARM external-provider lifecycle contract for ephemeral TrueNAS workloads while keeping TrueNAS-specific safety decisions outside GARM core.

Canonical cross-project terminology is defined in `SemperSupra/truenas-app-foundry/docs/terminology.md`. In this provider, **runtime realization** means actual creation/management of a runner workload on a TrueNAS runtime target. **Materialization** is reserved for the broader Foundry source-rendering → target-lowering pipeline.

## Runtime-backend architecture

The long-term provider is one external-provider package with three independently qualified TrueNAS runtime-realization backends:

1. **Apps backend** — TrueNAS Apps / Custom App / Docker Compose. This is the current implementation and the only backend under Stage 5 HIL.
2. **Containers backend** — future TrueNAS-managed Linux system containers through the supported release-specific middleware Container/Instances API.
3. **VM backend** — future TrueNAS-managed virtual machines through the supported release-specific `vm.*` middleware API.

The eventual GARM TrueNAS controller App should package the `garm-provider-truenas` executable in the same filesystem as GARM. Stock GARM external providers are local executables, so a separate provider App is not required or assumed. The same provider executable may later be declared to GARM multiple times with backend-specific config files (for example `truenas-apps`, `truenas-containers`, and `truenas-vms`), giving each runtime independent pools/scale sets, priorities, flavors, limits, and timeouts.

Use supported TrueNAS middleware APIs for every backend. Do not bypass middleware by driving Docker, Incus/LXC, libvirt, or qemu internals directly.

## Current Apps MVP contract

- Linux x86-64 only.
- Execution profile: `truenas-linux-general`.
- One job per ephemeral runner workload.
- Fixed image/resource/security profile; arbitrary Compose is not accepted from GARM.
- Exact allowlisted GitHub runner tool contract: tested version, official release URL/filename, and SHA-256 must match before runtime realization proceeds.
- Container-native JIT bootstrap; no VM/cloud-init/systemd dependency.
- Official runner payload is checksum-verified before extraction and must include `run.sh`, `Runner.Listener`, and Node 24.
- JIT credential bytes are isolated on a credential-only `noexec` tmpfs; runner binaries and the Actions work tree remain executable on the one-job container layer.
- Provider-owned naming and controller/pool ownership checks.
- Idempotent create/adopt behavior.
- Active, foreign-owned, or insufficiently understood workloads are never destructively removed.
- TrueNAS deletion preserves `ixVolume` datasets when `remove_ix_volumes=false`; the ephemeral Apps runner profile must not depend on persistent Compose volumes because TrueNAS delete performs Compose teardown with volume removal.
- No Docker socket, host paths, privileged mode, SSH injection, arbitrary proxy/CA mutation, or private site configuration in the fixed MVP profile.

## Desired state versus observed runtime state

Persisted custom Compose is **desired-state/ownership evidence**, not the only source of runtime truth. TrueNAS `app.query` exposes observed active workloads including container IDs, images, states, ports, mounts, networks, and health-derived App state. Future hardening should combine:

- desired-state verification: ownership labels, configured image/profile/security contract;
- observed-runtime attestation: expected container count/image/state and absence of forbidden mounts/ports/runtime drift.

Destructive retirement should revalidate ownership and observed inactive workload state immediately before `app.delete` rather than trusting a stale prior state transition.

## Public qualification boundary

GitHub-hosted public CI exercises formatting, module integrity, vet, race tests, provider build, GARM contract tests, bootstrap/callback tests, and a real Docker smoke of the exact pinned runtime image plus the checksummed official runner payload and Node 24 runtime. Public workflows reject self-hosted runner labels and private TrueNAS access.

This qualification proves generic code and container behavior; it does not claim real TrueNAS runtime realization, networking, App lifecycle timing, GitHub runner registration, scale-set behavior, or target-host cleanup. Those require private hardware-in-loop qualification.

## Release-specific compatibility profiles

Apps, Containers, and VMs are separate runtime targets with different middleware surfaces and lifecycle semantics. Each backend must bind qualification to an exact TrueNAS release/profile and upstream middleware identity rather than assuming all 25.04.x releases behave equivalently.

A backend compatibility profile should record the API methods/schemas used, state mapping, resource/admission behavior, image/storage/network behavior, bootstrap path, cleanup semantics, unsupported capabilities, and HIL evidence.

Containers and VMs remain future work. Do not claim nested Docker, Windows, GPU, or any Container/VM runner capability without backend-specific qualification evidence.

## GARM timing and resource model

GARM already provides provider-wide `exec_timeout_seconds` plus pool/scale-set `runner_bootstrap_timeout`, `max_runners`, `image`, `flavor`, `extra_specs`, and priority. The TrueNAS integration should use these to expose **qualified execution profiles** instead of arbitrary raw resource/security passthrough.

Different TrueNAS runtime backends will need different measured timing envelopes. Operation-specific deadlines belong inside the provider, with outer deadlines ordered so cleanup/reconciliation has headroom before GARM kills the provider process.

## Reference-provider strategy

Existing GARM providers are behavioral references rather than codebases to clone blindly. In particular, `cloudbase/garm-provider-lxd` demonstrates one provider supporting both containers and VMs, ownership tagging, image/flavor/profile mapping, current `garm-provider-common` tool/bootstrap semantics, asynchronous lifecycle handling, and reconciliation. Its transport is LXD-specific and its implementation is AGPL-3.0, so the TrueNAS provider should remain a clean implementation against the GARM provider-common interface and supported TrueNAS middleware APIs unless a separate licensing decision is made.

VM-oriented GARM providers such as OpenStack, AWS, Azure, GCP, OCI, and CloudStack are useful references for asynchronous VM lifecycle, eventual consistency, fault normalization, and cleanup. The Kubernetes provider is a useful reference for ephemeral container workload semantics.

## Foundry boundary

The provider is a native TrueNAS runtime-realization component, not the general Foundry source materializer and not a general multi-runtime target adapter. Future Foundry targets such as Docker Compose, Podman/Quadlet, Kubernetes/k3s, Nomad, or OCI/containerd require separate target-lowering adapters and qualification.

The three TrueNAS provider backends are different **runtime-realization backends within the native TrueNAS runtime family**, not Foundry target-lowering adapters.

## Sequencing

1. Finish Apps backend Stage 5 HIL at capacity one.
2. Harden Apps runtime attestation, lifecycle timing, and controller packaging from HIL evidence.
3. Build an exact TrueNAS compatibility profile and then prototype/qualify the Containers backend.
4. Build an exact TrueNAS compatibility profile and then prototype/qualify the VM backend.
5. Add cross-runtime selection/policy only after multiple backends have qualification evidence.

The current Stage 5 HIL scope is unchanged by this roadmap.

## Known Apps MVP limitation

TrueNAS 25.04 custom Apps have no documented post-create exec or ephemeral secret-injection interface. The GARM bootstrap JWT therefore appears transiently in custom Compose environment configuration. It stops being accepted after readiness, but dead token text may remain until the one-job App is retired. Zero-residue secret delivery is explicitly post-MVP work and is not claimed here.
