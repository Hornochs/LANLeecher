# 0006 – The beamer view renders the board API

**Status:** accepted

## Context

Organizers want the ranking on the beamer, in OBS overlays, in Discord and on second screens. If the
beamer page had its own logic, these outputs would drift apart, and every new output would need
access to client details.

## Decision

`/board` is a static page whose script renders `GET /api/v1/board` and follows
`GET /api/v1/board/stream` (Server-Sent Events). The payload is built in one place
(`internal/board`) and never contains MACs, IPs or hidden entries. A dedicated scope `read:board`
allows tokens for overlays only; `board.public: true` serves the board without authentication.

## Consequences

- Every overlay or bot sees exactly what the beamer shows.
- Devices without a hostname or display name appear as `Client #<id>`, never with their MAC.
- Rank changes are computed relative to the previous poll on the server, the same for all viewers.
