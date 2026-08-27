# GARM Provider for TrueNAS

Public source/release repository for a GARM external provider that materializes ephemeral GitHub Actions runners as TrueNAS workloads through the supported TrueNAS API.

This repository is deliberately public and inspectable. It contains sanitized provider source, public API/contracts, ordinary tests, mocked TrueNAS integration tests, documentation, release tooling, and provenance evidence.

It is **not** the normal development authority. Authoritative development occurs in `SemperSupra/garm-provider-truenas-private`, and public candidates are projected constructively from reviewed immutable private candidates.

## MVP

The first supported path is intentionally narrow:

- GARM external-provider contract;
- Linux/x86-64 only;
- one approved `truenas-linux-general` execution profile;
- one ephemeral TrueNAS Custom App per job;
- no host Docker socket, host mounts, or privileged mode;
- provider-side ownership, resource ceilings, idempotency, reconciliation, and safe-retirement checks;
- public CI on GitHub-hosted runners using a mocked TrueNAS JSON-RPC service;
- real TrueNAS hardware-in-loop testing only as a later private qualification gate.

Public workflows must not receive credentials that can read private repositories or access a private TrueNAS host.
