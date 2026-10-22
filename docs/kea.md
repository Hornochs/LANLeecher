# Kea DHCP

With a [Kea](https://www.isc.org/kea/) DHCP server, LANLeecher gets every client's IP address and the
hostname the client reported when it asked for a lease – usually the most reliable name source at a
LAN party. Kea is optional: without it, ARP, NetBIOS, mDNS, LLMNR and reverse DNS keep working.

## What LANLeecher does with Kea

- It reads leases with `lease4-get-page` / `lease6-get-page` every 30 seconds (configurable), page by
  page, and records MAC → IP and hostname with the source `dhcp`.
- It **only reads**. The commands it can send are fixed in the code: `version-get`, `list-commands`,
  `lease4-get-page`, `lease6-get-page`. Nothing else is ever sent.
- It only records leases of MAC addresses that were seen on a switch port. New MAC/IP pairs trigger
  NetBIOS and mDNS queries right away.
- DHCP hostnames rank first by default (`resolver.hostname_priority`).

## Kea versions

| Kea version | API endpoint | Kind in LANLeecher |
| --- | --- | --- |
| before 3.0 (e.g. 2.4, 2.6) | Kea Control Agent (`kea-ctrl-agent`), one URL for DHCPv4 and DHCPv6 | **Control Agent (Kea 2.x)**; tick DHCPv4 and/or DHCPv6 |
| 3.0 and later | HTTP(S) control socket of the DHCP server itself | **DHCPv4 server directly** or **DHCPv6 server directly** – one source each |

Kea 3.0 removed the Control Agent. If you upgrade Kea, change the source kind and URL.

In both cases Kea must load the **lease commands hook** (`libdhcp_lease_cmds.so`, part of the
open-source Kea packages) in the DHCP server.

## Kea 2.x: Control Agent

`kea-ctrl-agent.conf` (TLS and basic authentication; adjust paths to your distribution):

```json
{
  "Control-agent": {
    "http-host": "192.0.2.5",
    "http-port": 8000,
    "trust-anchor": "/etc/kea/tls/ca.pem",
    "cert-file": "/etc/kea/tls/kea.crt",
    "key-file": "/etc/kea/tls/kea.key",
    "cert-required": false,
    "authentication": {
      "type": "basic",
      "realm": "kea-control-agent",
      "clients": [ { "user": "lanleecher", "password-file": "/etc/kea/lanleecher.password" } ]
    },
    "control-sockets": {
      "dhcp4": { "socket-type": "unix", "socket-name": "/run/kea/kea4-ctrl-socket" },
      "dhcp6": { "socket-type": "unix", "socket-name": "/run/kea/kea6-ctrl-socket" }
    }
  }
}
```

The Control Agent only enables TLS when `trust-anchor`, `cert-file` and `key-file` are all set;
`cert-required: false` means LANLeecher does not need a client certificate. In `kea-dhcp4.conf` (and
`kea-dhcp6.conf`), the control socket and the hook:

```json
"control-socket": { "socket-type": "unix", "socket-name": "/run/kea/kea4-ctrl-socket" },
"hooks-libraries": [ { "library": "/usr/lib/x86_64-linux-gnu/kea/hooks/libdhcp_lease_cmds.so" } ]
```

In LANLeecher: **Admin → DHCP sources → Add DHCP source**, URL `https://192.0.2.5:8000/`, kind
**Control Agent (Kea 2.x)**, DHCPv4 (and DHCPv6), user `lanleecher` and the password.

## Kea 3.x: control socket of the DHCP server

In `kea-dhcp4.conf` an HTTPS control socket next to the UNIX socket (DHCPv6 analogously with its own
port):

```json
"control-sockets": [
  { "socket-type": "unix", "socket-name": "/run/kea/kea4-ctrl-socket" },
  {
    "socket-type": "https",
    "socket-address": "192.0.2.5",
    "socket-port": 8004,
    "trust-anchor": "/etc/kea/tls/ca.pem",
    "cert-file": "/etc/kea/tls/kea.crt",
    "key-file": "/etc/kea/tls/kea.key",
    "cert-required": false,
    "authentication": {
      "type": "basic",
      "realm": "kea-dhcpv4-server",
      "clients": [ { "user": "lanleecher", "password-file": "/etc/kea/lanleecher.password" } ]
    }
  }
],
"hooks-libraries": [ { "library": "/usr/lib/x86_64-linux-gnu/kea/hooks/libdhcp_lease_cmds.so" } ]
```

Option names can change between Kea releases – compare with the Kea Administrator Reference Manual of
your version. In LANLeecher: URL `https://192.0.2.5:8004/`, kind **DHCPv4 server directly
(Kea 3.x)**; add a second source for DHCPv6 if you use it.

## Certificates

LANLeecher accepts HTTPS only (plain HTTP for `localhost` only) with at least TLS 1.2, and verifies the
certificate during the TLS handshake – before user name and password are sent. Three ways to trust a
certificate:

1. **Publicly trusted or internal CA in the system store:** nothing to do; the hostname must match.
2. **Paste a certificate:** your internal CA, or Kea's self-signed certificate, under "Trusted
   certificate (PEM)". The hostname must match.
3. **Trust with one click:** leave the field empty and press **Test connection**. LANLeecher shows a
   warning page like a browser – reason, subject, issuer, names, validity and SHA-256 fingerprint.
   Compare the fingerprint on the Kea host:

   ```bash
   openssl x509 -in /etc/kea/tls/kea.crt -noout -fingerprint -sha256
   ```

   **Trust this certificate** stores exactly this certificate; from then on every other certificate is
   rejected, regardless of hostname or CA.

When a trusted certificate changes (e.g. after a renewal), LANLeecher stops polling that source,
shows a warning on the dashboard and offers the old and the new fingerprint side by side. Only another
click accepts the new certificate. The detail page warns 14 days before the trusted certificate
expires. Every acceptance and revocation is written to the audit log with the fingerprint; only
admins can do it.

Optional mutual TLS: paste a client certificate and its private key, and set `cert-required: true`
in Kea. "Do not verify the certificate" exists for emergencies only; it is shown as a warning in the
UI and the log.

## Credentials

Password and client key are stored AES-256-GCM encrypted (like SNMP communities), are never shown
again and never appear in logs, the audit log or API responses.

Open-source Kea has no per-command permissions for API users: whoever knows the password can also
change the configuration. LANLeecher restricts itself to reading, but protect the account anyway:

- create a **dedicated Kea user** for LANLeecher with a long random password;
- let the API listen on a management address only, and allow the LANLeecher host in your firewall;
- consider `cert-required: true` with a client certificate for LANLeecher.

## Troubleshooting

| Message | Cause |
| --- | --- |
| `authentication failed` | wrong user or password, or `authentication` missing in Kea while LANLeecher sends credentials |
| `the lease_cmds hook … is not loaded` | add `libdhcp_lease_cmds.so` to `hooks-libraries` of the DHCP server and restart it |
| `forwarding socket is not configured for the server type dhcp6` | the Control Agent has no `dhcp6` control socket – untick DHCPv6 or configure it |
| `server certificate not trusted: x509: certificate is valid for …, not …` | the URL's host is not in the certificate – use the right name or trust the certificate with one click |
| `connection refused` / timeout | wrong address or port, Kea not listening on that address, or a firewall |

## Trying it without Kea

`tools/keasim` simulates both API variants with the leases of the demo switch:

```bash
go run ./tools/keasim -mode agent -user lanleecher -password secret-password   # Kea 2.x, https://127.0.0.1:8000/
go run ./tools/keasim -mode dhcp4 -addr 127.0.0.1:8004                          # Kea 3.x
```

It prints its certificate fingerprint and creates a new certificate on every start – restart it to
see the "certificate changed" warning.
