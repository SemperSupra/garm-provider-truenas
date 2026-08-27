# Three TrueNAS runtime backends for GARM

## Decision

The GARM TrueNAS integration will be designed around **one external-provider package with three independently qualified runtime-realization backends**:

1. **Apps backend** — TrueNAS Apps / Custom App / Docker Compose realization. This is the current implementation and the only backend in Stage 5 HIL.
2. **Containers backend** — future TrueNAS-managed Linux system-container realization through the supported middleware Container/Instances API for the exact target release.
3. **VM backend** — future TrueNAS-managed virtual-machine realization through the supported `vm.*` middleware API for the exact target release.

The backends share GARM protocol handling and common TrueNAS integration infrastructure where semantics are genuinely common, but they are not interchangeable implementations of the same runtime.

## Packaging model

The eventual GARM TrueNAS App should package:

```text
GARM TrueNAS App
├── GARM controller
├── /opt/garm/providers.d/garm-provider-truenas
├── persistent GARM/provider configuration and controller state
└── backend-specific provider configuration
     ├── truenas-apps.toml
     ├── truenas-containers.toml
     └── truenas-vms.toml
```

Upstream GARM external providers are local executables. The TrueNAS provider binary therefore belongs in the same controller App/container filesystem as GARM. A separate provider App would not satisfy the stock external-provider interface without changing GARM.

The same executable may be declared to GARM multiple times with different provider names/config files:

```toml
[[provider]]
name = "truenas-apps"
provider_type = "external"
[provider.external]
provider_executable = "/opt/garm/providers.d/garm-provider-truenas"
config_file = "/etc/garm/providers/truenas-apps.toml"

[[provider]]
name = "truenas-containers"
provider_type = "external"
[provider.external]
provider_executable = "/opt/garm/providers.d/garm-provider-truenas"
config_file = "/etc/garm/providers/truenas-containers.toml"

[[provider]]
name = "truenas-vms"
provider_type = "external"
[provider.external]
provider_executable = "/opt/garm/providers.d/garm-provider-truenas"
config_file = "/etc/garm/providers/truenas-vms.toml"
```

This preserves normal GARM semantics: each runtime can have independent pools/scale sets, priorities, max runners, bootstrap timeouts, provider execution timeouts, images, flavors, and extra specs.

## Backend model

Conceptually the provider should evolve toward a shared core plus backend-specific adapters:

```text
cmd/garm-provider-truenas
│
├── GARM external-provider contract
├── TrueNAS versioned JSON-RPC transport
├── release/capability discovery
├── ownership and reconciliation
├── common runner-tool/bootstrap helpers
└── runtime backend
     ├── apps
     ├── container
     └── vm
```

A future internal interface may resemble:

```go
type RuntimeBackend interface {
    ValidateProfile(...)
    Create(...)
    Get(...)
    List(...)
    Start(...)
    Stop(...)
    Delete(...)
    Reconcile(...)
    Capabilities(...)
}
```

This is an architectural direction, not a request to refactor the current Apps implementation before Stage 5 HIL.

## Backend characteristics

| Backend | TrueNAS control surface | Isolation / behavior | Intended use |
| --- | --- | --- | --- |
| Apps | `app.*`, Custom App, Docker Compose | OCI container; shared host kernel; fastest realization | trusted/simple Linux Actions workloads |
| Containers | release-specific TrueNAS Container/Instances middleware API | Linux system container; shared kernel but more machine-like userspace | general Linux runner workloads requiring a fuller OS environment |
| VMs | release-specific `vm.*` middleware API | hardware virtualization; full guest kernel | strongest isolation, Docker/systemd/kernel-sensitive workloads, future Windows/GPU candidates |

The provider must use supported middleware APIs. It must not bypass TrueNAS ownership by talking directly to Docker, Incus/LXC, libvirt, or qemu merely because those technologies exist underneath the middleware.

## Capability ladder

The three backends are not substitutes with equal semantics. Qualification should expose explicit capabilities.

### Apps

Potential baseline capabilities:
- shell/script jobs;
- JavaScript/Node actions;
- composite actions;
- normal compiler/build/test jobs that fit the fixed container security envelope.

Do not claim Docker container actions, privileged workloads, nested Docker, or host integration under the current fixed Apps profile.

### Containers

Potential future capabilities:
- fuller Linux userspace;
- package installation;
- service-oriented workloads where supported by the exact system-container profile;
- broader toolchain compatibility than the single App container.

Nested Docker/container-engine support must be separately researched and HIL-qualified; it is not assumed.

### VMs

Potential future capabilities:
- full guest kernel and systemd;
- conventional Docker daemon/container actions;
- stronger workload isolation;
- kernel-sensitive tooling;
- future Windows profiles;
- future GPU/device passthrough profiles.

Windows, GPU, and Docker-capable profiles require separate qualification evidence.

## Qualified flavors, not arbitrary knobs

GARM's provider-neutral abstraction already gives us `image`, `flavor`, `extra_specs`, `max_runners`, priorities, and bootstrap timeout. The TrueNAS provider should expose **qualified execution profiles**, not arbitrary raw CPU/RAM/security passthrough.

Candidate naming:

```text
truenas-app-linux-small
truenas-app-linux-general

truenas-container-linux-small
truenas-container-linux-general
truenas-container-linux-heavy

truenas-vm-linux-small
truenas-vm-linux-general
truenas-vm-linux-heavy
truenas-vm-linux-docker

future only:
truenas-vm-windows
truenas-vm-gpu
```

The current `truenas-linux-general` Apps profile remains the MVP compatibility name until a deliberate migration is designed. Do not rename the active Stage 5 profile merely to align future naming.

## TrueNAS release compatibility

Runtime availability and API semantics have changed across TrueNAS releases, especially in the 25.04 family. Each backend therefore requires an exact compatibility profile tied to an observed TrueNAS release and upstream middleware identity.

The compatibility profile must record at minimum:
- TrueNAS release/version;
- middleware ref/commit;
- API methods and schemas relied upon;
- lifecycle state model;
- resource/admission behavior;
- image/storage/network behavior;
- bootstrap mechanism;
- deletion/cleanup semantics;
- known unsupported capabilities;
- HIL evidence.

Do not infer that all 25.04.x releases provide equivalent Container or VM behavior. In particular, do not spend implementation effort supporting transitional VM-via-Instances behavior merely for compatibility if the target release provides the classic `vm.*` API.

## GARM timing and capacity

Each GARM provider definition may use independent `exec_timeout_seconds`, pools/scale sets, `max_runners`, priorities, and `runner_bootstrap_timeout`. That is desirable because the three TrueNAS runtimes have materially different realization latency and capacity cost.

Do not use one timeout profile for all backends. Each compatibility/qualification profile should establish measured timing envelopes and leave nested deadline headroom:

```text
backend operation deadline
    < provider command deadline
    < GARM external-provider exec timeout
    < runner bootstrap timeout
```

Operation-specific timeouts belong inside the provider because GARM's `exec_timeout_seconds` is provider-wide.

## Relationship to existing GARM providers

Use existing providers as behavioral references:

- `cloudbase/garm-provider-lxd`: strongest reference for local container/VM duality, ownership tags, image/flavor/profile semantics, cloud-init bootstrap, async operations, and lifecycle reconciliation.
- Kubernetes provider: useful reference for ephemeral container workload semantics.
- OpenStack/AWS/Azure/GCP/OCI: useful references for VM lifecycle, asynchronous operations, eventual consistency, fault normalization, and cleanup.

`garm-provider-lxd` is AGPL-3.0 and uses LXD-specific transport/data models. Reuse architecture and behavior as a reference; do not copy/fork it wholesale into the TrueNAS provider without an explicit licensing decision.

## Sequencing

The project sequence remains deliberately narrow:

1. Finish **Apps backend Stage 5 HIL** at capacity one.
2. Harden Apps runtime attestation, lifecycle timing, and production packaging based on HIL evidence.
3. Build a release-specific **Containers compatibility profile**, then prototype/qualify the Containers backend.
4. Build a release-specific **VM compatibility profile**, then prototype/qualify the VM backend.
5. Only after multiple backends are qualified, add cross-runtime policy/selection and portfolio scheduling.

GARM can already model these as separate provider definitions and pools/scale sets. We do not need to modify GARM core merely to support three TrueNAS runtimes.

## Current non-claims

As of this decision:
- Apps is implemented but real-NAS HIL remains pending.
- Containers is architecture/future work only.
- VMs is architecture/future work only.
- No automatic runtime selection is implemented.
- No nested-Docker, Windows, or GPU profile is qualified.
- The current Stage 5 owner/live-mutation gate remains unchanged.

Tracking:
- architecture authority: `SemperSupra/garm-provider-truenas-private#13`
- public projection: `SemperSupra/garm-provider-truenas#12`
- Containers future stage: `SemperSupra/garm-provider-truenas-private#14`
- VM future stage: `SemperSupra/garm-provider-truenas-private#15`
