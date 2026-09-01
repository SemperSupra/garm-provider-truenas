# Callback host-gateway option

The Apps backend has one narrow opt-in networking policy for deployments where the GARM controller and ephemeral runner Apps share the same Docker host but household/site DNS must remain independent of that host.

Provider configuration:

```json
{
  "mode": "truenas",
  "truenas": {
    "host": "truenas.example.invalid",
    "username": "provider-service",
    "callback_host_gateway": true
  }
}
```

`callback_host_gateway` defaults to `false`.

When disabled, generated runner Compose is unchanged and contains no `extra_hosts` entry.

When enabled, the provider:

1. uses the already-authoritative GARM callback and metadata HTTPS URLs supplied in the bootstrap contract;
2. requires both URLs to use the same non-empty DNS hostname;
3. rejects non-HTTPS URLs and IP-literal hosts;
4. derives exactly one Compose mapping, `<callback-hostname>:host-gateway`;
5. leaves the callback and metadata URLs unchanged so ordinary TLS SNI/hostname verification is preserved;
6. records the enabled policy in provider-owned runner metadata; and
7. refuses to adopt managed Apps whose stored mapping does not exactly match the hostname derived from their stored callback/metadata URLs.

This is not a general host-map feature. There is no caller-supplied hostname/address map, no raw LAN-IP target, no resolver override, no custom CA, no TLS disable switch, and no proxy behavior associated with this option.

The option is specific to the TrueNAS Apps backend. It does not change mock mode and does not imply equivalent networking behavior for future Containers or VM backends.
