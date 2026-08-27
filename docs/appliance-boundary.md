# Provider boundary inside the GARM TrueNAS appliance

`garm-provider-truenas` is an **additive external provider** for GARM. It is not the whole GARM provider ecosystem and it must not redefine stock GARM scheduling, entity, credential, or account-plan behavior.

The general TrueNAS controller package is defined in `SemperSupra/truenas-app-foundry/docs/garm-appliance-architecture.md`.

## Scope owned by this provider

This repository owns only the TrueNAS realization path:

```text
GARM external-provider contract
        |
        v
garm-provider-truenas
        |
        +-- Apps backend          current
        +-- Containers backend    future
        +-- VM backend            future
```

It owns:

- translation from the GARM external-provider contract into supported TrueNAS middleware operations;
- TrueNAS-specific ownership/reconciliation rules;
- TrueNAS execution profiles/flavors;
- runner bootstrap details needed by each TrueNAS backend;
- TrueNAS lifecycle/state mapping and fail-closed behavior;
- backend-specific compatibility and HIL qualification.

It does **not** own:

- upstream AWS/Azure/GCP/OCI/OpenStack/LXD/Incus/Kubernetes provider behavior;
- GARM endpoint/entity/credential semantics;
- GitHub Runner Scale Set scheduling;
- GARM webhook Pool scheduling/balancing;
- GitHub account-plan entitlements or runner-group policy;
- cross-provider portfolio policy above the provider boundary.

## Coexistence with upstream providers

The TrueNAS appliance should preserve the pinned upstream GARM provider bundle and add this provider beside it.

A valid controller may therefore have provider definitions such as:

```text
aws-production
oci-overflow
incus-lab
truenas-apps
truenas-containers     future
truenas-vms            future
```

Nothing in this provider requires a TrueNAS backend to be selected for every GARM entity or workflow.

The presence of `garm-provider-truenas` must not shadow, replace, or require removal of another provider executable.

Likewise, preserving an upstream local provider must not force extra privileges into this provider or the controller App. The TrueNAS provider uses supported middleware APIs and does not require direct host Docker/Incus/libvirt sockets.

## Scheduling boundary

GARM Runner Scale Sets and webhook-driven Pools sit **above** this provider.

From this provider's perspective, both eventually result in GARM invoking the same external-provider lifecycle operations with the selected image/flavor/bootstrap context.

Therefore:

- Scale Set vs Pool selection is not encoded as a TrueNAS runtime backend;
- GitHub Free/Team/Enterprise policy is not implemented inside this provider;
- runner-group creation or plan capability must not become a provider prerequisite;
- one provider binary may service multiple GARM provider definitions/pools/scale sets as configured by GARM.

Provider timeout/resource profiles may differ by TrueNAS backend, but the higher-level scheduler remains GARM.

## Three TrueNAS backends remain separate

The additive-appliance decision does not change the existing backend qualification sequence:

1. Apps backend — current implementation; Stage 5 HIL remains the only active HIL target.
2. Containers backend — future release-specific implementation/qualification.
3. VM backend — future release-specific implementation/qualification.

No controller-packaging decision constitutes support for an unimplemented backend.

## Qualification claims

This repository may claim only what its own evidence proves.

A provider PASS can establish that a TrueNAS backend satisfies the GARM external-provider contract on an exact target profile. It does not establish that:

- every upstream provider packaged with GARM works;
- a GitHub plan exposes a specific runner-group feature;
- a Scale Set or Pool is correctly configured for every organization;
- the complete GARM controller TrueNAS App has passed its separate packaging/HIL gates.

Those claims belong to their respective qualification authorities.

## Current sequencing

The controller-appliance architecture is follow-on product work and must not broaden or delay the current capacity-one Apps Stage 5 HIL.

Provider implementation priorities remain:

- finish Apps HIL;
- harden observed-runtime/lifecycle behavior from HIL evidence;
- later add Containers and VM backends behind their own compatibility profiles;
- only then consider higher-level cross-runtime/provider selection features.
