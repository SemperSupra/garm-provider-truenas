# GARM Provider for TrueNAS

Public source/release repository for a GARM external provider that **realizes** ephemeral GitHub Actions runners as TrueNAS workloads through the supported TrueNAS API.

This repository is deliberately public and inspectable. It contains sanitized provider source, public API/contracts, ordinary tests, mocked TrueNAS integration tests, documentation, release tooling, and provenance evidence.

It is **not** the normal development authority. Authoritative development occurs in `SemperSupra/garm-provider-truenas-private`, and public candidates are projected constructively from reviewed immutable private candidates.

Canonical cross-project terminology is defined in `SemperSupra/truenas-app-foundry/docs/terminology.md`. In provider documentation, **runtime realization** means actual TrueNAS App creation/startup; **materialization** is reserved for the broader Foundry source-rendering → target-lowering pipeline.

## MVP

The supported path is intentionally narrow:

- GARM external-provider contract;
- Linux/x86-64 only;
- one approved `truenas-linux-general` execution profile;
- one ephemeral TrueNAS Custom App per job;
- fixed pinned runtime image and exact checksummed official GitHub runner tool contract;
- container-native JIT bootstrap with credential bytes isolated on a `noexec` tmpfs;
- no host Docker socket, host mounts, or privileged mode;
- provider-side ownership, resource ceilings, idempotency, reconciliation, and safe-retirement checks;
- public CI on GitHub-hosted runners covering formatting, module integrity, vet, race tests, build, mocked GARM/TrueNAS behavior, and a real Docker smoke of the pinned image plus verified runner payload and Node 24;
- real TrueNAS hardware-in-loop runtime-realization testing remains a private qualification gate before any portfolio pilot.

Public CI proves the generic provider and container/bootstrap contract. It does not claim real TrueNAS runtime realization, networking, App lifecycle timing, GitHub runner registration, target-host cleanup, or scale-set reliability.

The provider is a native TrueNAS runtime-realization component, not the general Foundry source materializer and not a multi-runtime target-lowering adapter. Future Foundry targets require separate adapters and qualification.

Public workflows must not receive credentials that can read private repositories or access a private TrueNAS host.
