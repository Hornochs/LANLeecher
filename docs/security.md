# Security model

How to report a vulnerability: see [SECURITY.md](../SECURITY.md).

## What LANLeecher holds

| Asset | Where | Protection |
| --- | --- | --- |
| SNMP communities, v3 passwords | database | AES-256-GCM, key in a separate file (`secret.key`, mode 0640) |
| User passwords | database | Argon2id (m=19 MiB, t=2, p=1) |
| Sessions, API tokens | database | only SHA-256 hashes are stored |
| MAC/IP/hostname history, traffic volumes of guests | database | login for every page; `read:clients` scope for the API |

A copy of the database alone reveals no credentials. With database **and** `secret.key`, the SNMP
credentials can be decrypted – back up both, but keep them away from untrusted places.

## Threats and mitigations

| Threat | Mitigation |
| --- | --- |
| Guests on the LAN guess passwords | login lockout (5 failures per 10 min per IP and per user name → 5 min), Argon2id, no default account |
| Malicious hostnames or `ifAlias` values (stored XSS) | all output escaped by `html/template`; CSP `script-src 'self'` without inline scripts; board JS uses `textContent` |
| CSV injection through hostnames in exports | values starting with `= + - @` are prefixed with `'` |
| Cross-site request forgery | per-session CSRF token on every POST; `SameSite=Lax` cookies; login form with double-submit token |
| Clickjacking | `X-Frame-Options: DENY`, `frame-ancestors 'none'` |
| Stolen API token | scopes, optional expiry and IP allowlist, immediate revocation, `last_used` display, rate limit |
| MAC addresses leaking through public views | board and `read:rankings` never show MACs/IPs; devices without a name appear as `Client #<id>` |
| Spoofed client IPs in logs/limits | `X-Real-IP`/`X-Forwarded-For` only from `trusted_proxies` |
| Compromised service | systemd unit without capabilities, `ProtectSystem=strict`, only `/var/lib/lanleecher` writable |
| Credentials in logs | communities and passwords are never logged, audited, returned by the API or pre-filled in forms |

## Residual risks

- **SNMP v2c is unencrypted.** Anyone on the path between LANLeecher and a switch can read the
  community. Use a dedicated read-only community restricted to the LANLeecher host, a management
  VLAN, or SNMP v3 with `authPriv`.
- **Name probes are unauthenticated.** NetBIOS, mDNS and LLMNR answers can be forged by anyone on the
  LAN; hostnames are informational only and never merge devices on their own.
- **MAC addresses can be spoofed.** A guest can take another guest's MAC; LANLeecher cannot tell.
- **Plain HTTP without a proxy** sends session cookies unencrypted. Use TLS (reverse proxy or
  built-in) whenever the UI is reachable from the guest network.

## Out of scope

- Vulnerabilities in switches or their SNMP agents.
- Attacks that require an already compromised admin account or root on the LANLeecher host.
- Denial of service through traffic volume on the local network.
