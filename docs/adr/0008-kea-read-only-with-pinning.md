# 0008 – Kea: both API generations, read-only, certificates trusted by an admin

**Status:** accepted

## Context

Kea DHCP knows the client-reported hostname of every guest, the best name source at a LAN party.
Kea changed its management API with version 3.0: the Control Agent (one endpoint, a `service` list,
answers wrapped in a list) was removed, and the DHCP servers got their own HTTP(S) control sockets.
Many installations still run Kea 2.x. Kea servers at LAN parties often use self-signed certificates,
and open-source Kea cannot restrict an API user to read-only commands.

## Decision

- A DHCP source has a kind: `agent` (Kea < 3) or `dhcp4`/`dhcp6` (Kea ≥ 3). Responses are accepted as a
  list or a single map for both.
- The client only sends commands from a fixed allowlist (`version-get`, `list-commands`,
  `lease4-get-page`, `lease6-get-page`).
- HTTPS only (HTTP for localhost), TLS 1.2 or newer, no proxy, no redirects. The certificate is
  verified in `VerifyConnection`, i.e. during the handshake, before credentials are sent.
- Trust comes from the system store, a pasted CA/server certificate, or a certificate an admin
  accepted with one click after seeing its details (pinning by SHA-256). A pinned certificate that
  changes stops polling until an admin accepts the new one.
- Leases are only used for MACs seen on switch ports; DHCP never creates devices.

## Consequences

- Kea 2.x and 3.x installations work without configuration changes on the Kea side beyond TLS,
  authentication and the lease commands hook.
- A self-signed Kea needs no CA infrastructure, and a certificate swap cannot silently redirect
  LANLeecher's credentials.
- Certificate renewals need one confirmation in the UI.
- Kea's API account could still change Kea's configuration if its password leaked; the docs recommend
  a dedicated account, a management network and optionally mutual TLS.
