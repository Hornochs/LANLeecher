# Security policy

LANLeecher stores SNMP credentials of network switches and personal data of LAN party guests (MAC
addresses, IP addresses, hostnames, traffic volumes). Security reports are taken seriously and
handled with priority.

## Supported versions

Only the latest release and the `main` branch receive security fixes.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report privately through GitHub: open the repository's **Security** tab and choose
**Report a vulnerability** (private vulnerability reporting). Include:

- what you found and where (file, endpoint, version or commit),
- how to reproduce it,
- what an attacker could achieve.

You can expect an acknowledgement within 7 days. Once a fix is available it is released together
with an advisory that credits you, unless you prefer to stay anonymous.

## Security model in brief

The threat model, mitigations and residual risks are in [docs/security.md](docs/security.md).

- Every page requires a login; the beamer view is public only if explicitly configured, and never
  shows MAC or IP addresses.
- User passwords are hashed with Argon2id; failed logins lead to a temporary lockout.
- SNMP credentials are encrypted at rest with a key stored outside the database.
- API tokens are stored as hashes, scoped, optionally limited by IP and expiry, and rate-limited.
- Everything a switch or client reports (interface descriptions, hostnames) is treated as
  untrusted: escaped output, Content Security Policy without inline scripts.

## Known limitation

SNMP v2c transmits the community **unencrypted**. Use a read-only community restricted to the
LANLeecher host, a management network, or SNMP v3 with `authPriv`.

## Out of scope

- Vulnerabilities in switches or their SNMP agents.
- Attacks that require an already compromised admin account or root on the LANLeecher host.
- Denial of service through traffic volume on the local network.
