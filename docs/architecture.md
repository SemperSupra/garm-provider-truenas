# GARM Provider for TrueNAS

This provider implements the GARM external-provider lifecycle contract for ephemeral TrueNAS workloads while keeping TrueNAS-specific safety decisions outside GARM core.

## MVP contract

- Linux x86-64 only.
- Execution profile: `truenas-linux-general`.
- One job per ephemeral runner workload.
- Fixed image/resource/security profile; arbitrary Compose is not accepted from GARM.
- Exact allowlisted GitHub runner tool contract: tested version, official release URL/filename, and SHA-256 must match before materialization proceeds.
- Container-native JIT bootstrap; no VM/cloud-init/systemd dependency.
- Official runner payload is checksum-verified before extraction and must include `run.sh`, `Runner.Listener`, and Node 24.
- JIT credential bytes are isolated on a credential-only `noexec` tmpfs; runner binaries and the Actions work tree remain executable on the one-job container layer.
- Provider-owned naming and controller/pool ownership checks.
- Idempotent create/adopt behavior.
- Active, foreign-owned, or insufficiently understood workloads are never destructively removed.
- TrueNAS deletion is explicitly non-volume-destructive.
- No Docker socket, host paths, privileged mode, SSH injection, arbitrary proxy/CA mutation, or private site configuration in the fixed MVP profile.

## Public qualification boundary

GitHub-hosted public CI exercises formatting, module integrity, vet, race tests, provider build, GARM contract tests, bootstrap/callback tests, and a real Docker smoke of the exact pinned runtime image plus the checksummed official runner payload and Node 24 runtime. Public workflows reject self-hosted runner labels and private TrueNAS access.

This qualification proves generic code and container behavior; it does not claim real TrueNAS networking, App lifecycle timing, GitHub runner registration, scale-set behavior, or target-host cleanup. Those require private hardware-in-loop qualification.

## Known MVP limitation

TrueNAS 25.04 custom Apps have no documented post-create exec or ephemeral secret-injection interface. The GARM bootstrap JWT therefore appears transiently in custom Compose environment configuration. It stops being accepted after readiness, but dead token text may remain until the one-job App is retired. Zero-residue secret delivery is explicitly post-MVP work and is not claimed here.
