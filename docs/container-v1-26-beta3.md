# TrueNAS 26 BETA.3 container-v1 compatibility profile

Authority: `SemperSupra/garm-provider-truenas-private#14`, rolled up by
`SemperSupra/garm-provider-truenas-private#43`.

This document is a source/static compatibility profile. It is **not** a runtime
support claim. The matrix cell remains `OPEN` until the B0-B5 runtime ladder
earns an exact receipt.

## Exact target

- TrueNAS: `26.0.0-BETA.3`
- middleware commit: `81e1265a86083888ba94a2bdfc02ff5c9c5ef6a3`
- driver: `container-v1`
- control surface: first-class `container.*`
- GARM profile: `truenas-container-linux-general`

Source identities:

- `plugins/container/container.py`:
  `54111667692fa7beea67893b50149ef605a8ce22`
- `plugins/container/lifecycle.py`:
  `1e29c7c3143d9adbd0cb8e03806a3d5f5ebe29e0`
- `plugins/container/image.py`:
  `43f01e991ceaacd46ca2292cc8cc6d460dc3dc7b`
- `api/v26_0_0/container.py`:
  `6c1d11b9d42d5bfc6dd242d77c017b7fbe1a26aa`

The machine-readable authority is
`internal/truenasstore/testdata/runtime-backend-target-matrix.json`.

## Required public middleware surface

The first implementation must observe and bind all of:

- `system.version`
- `container.query`
- `container.create`
- `container.update`
- `container.delete`
- `container.start`
- `container.stop`
- `container.image.query_registry`

No direct libvirt/LXC/Incus calls and no private `container.nsenter` control are
allowed in the provider.

## Realization model

The initial container is deliberately narrow:

- Linux amd64 only.
- Image family: TrueNAS container registry Ubuntu Noble
  (`ubuntu:noble:amd64:default`). Runtime qualification must resolve and
  receipt the exact registry version; a floating image-family name is not
  sufficient evidence.
- `autostart=false`. A one-job JIT runner must not silently restart after host
  reboot or middleware restart.
- normal TrueNAS default bridge networking unless qualification proves a
  narrower supported network contract.
- `idmap={type: DEFAULT}`; never use an un-idmapped container merely to make
  bootstrap easier.
- `capabilities_policy=DEFAULT` with no added capabilities for the general
  profile.
- ordinary init/bootstrap only; no privileged/nested-container assumption.

TrueNAS 26 exposes `cpuset`, but the lifecycle source explicitly leaves
vCPU/core/thread/memory values unset and notes that LXC does not respect the
memory field. Therefore this profile **does not advertise per-runner CPU or
memory isolation**. Capacity admission remains a GARM/host policy concern until
a supported, runtime-qualified resource-control surface exists.

## Ownership and reconciliation

Provider ownership must be recoverable from non-secret persisted metadata.
The implementation should encode, at minimum:

- managed-by/schema identity;
- GARM controller ID;
- GARM pool/Scale Set identity;
- execution profile;
- requested runner identity.

The bootstrap token and GitHub JIT credential material are not ownership
metadata and must never be used to identify/adopt a container.

Unknown ownership, malformed ownership, an unexpected runtime profile, or an
unrecognized exact TrueNAS version fails closed. No Container failure may fall
back to Apps.

## Bootstrap boundary

The public API has no cloud-init field and no public general-purpose exec
method. It does expose `init`/`initenv` plus `container.update`.

The minimum candidate bootstrap therefore uses the container's initial process
to run the existing provider-common/JIT bootstrap contract. If the ephemeral
GARM bootstrap token is lowered through `initenv`, the implementation must:

1. never include it in ownership metadata or durable evidence;
2. start the container exactly once;
3. after the process has inherited the environment, use `container.update`
   to remove the token from persisted `initenv`;
4. independently read back the container and prove the token is absent;
5. fail closed if scrub/read-back cannot be proven;
6. never restart a retired/stopped one-job runner with the old bootstrap
   contract.

Source inspection shows `container.update` changes datastore state without
restarting an already-running container except for specific configuration
side-effects. Runtime qualification must prove that the runner process keeps
the inherited bootstrap state while the persisted management-plane value is
scrubbed. Until then this is a candidate mechanism, not an accepted security
claim.

GitHub JIT credential files remain a separate boundary: they are fetched by the
runner bootstrap from GARM and must be deleted on exit. Public nested
qualification may use only synthetic/non-reusable material.

## State mapping and retirement

Source state is `RUNNING` or `STOPPED`. Provider behavior should map these
conservatively and treat unknown/malformed state as active/unsafe.

Retirement sequence for the first profile:

1. observe exact owned container and state;
2. stop if still running, using bounded `container.stop`;
3. independently read back STOPPED;
4. delete by exact container ID;
5. independently prove absence from `container.query`;
6. prove no provider-owned residual dataset/device state before accepting B5.

External Start after a stopped one-job JIT runner is unsafe and should remain
rejected unless a later profile explicitly re-bootstrap/reissues credentials.

## Qualification ladder

- B0: exact source/API/profile fingerprint — this document plus matrix contract.
- B1: deterministic desired realization and ownership encoding.
- B2: create plus independent read-back.
- B3: bootstrap/start, Get/List, adoption/reconnect.
- B4: bounded synthetic GitHub/JIT boundary in public nested TrueNAS.
- B5: stop/delete/reconciliation plus zero unintended residue.
- B6: one real capacity-one GitHub job on the selected physical TrueNAS target.

Nested Docker/container actions, privileged execution, GPU/USB passthrough,
arbitrary filesystem devices, and capacity greater than one are separate future
claims.
