# GARM Provider for TrueNAS

This provider implements the GARM external-provider lifecycle contract for ephemeral TrueNAS workloads while keeping TrueNAS-specific safety decisions outside GARM core.

## MVP contract

- Linux x86-64 only.
- Execution profile: `truenas-linux-general`.
- One job per ephemeral runner workload.
- Fixed image/resource/security profile; arbitrary Compose is not accepted from GARM.
- Provider-owned naming and controller/pool ownership checks.
- Idempotent create/adopt behavior.
- Active or foreign-owned workloads are never destructively removed.
- Live TrueNAS mode fails closed until the WebSocket JSON-RPC transport passes protocol and hardware-in-loop qualification.

Public CI uses a file-backed mock backend to exercise the GARM command surface without any access to a private NAS.
