# Contributing to LANLeecher

Thanks for your interest. The most valuable contributions are reports and adapters for switches
that behave differently from the standard MIBs – see [docs/adapters.md](docs/adapters.md) and
[docs/switch-compatibility.md](docs/switch-compatibility.md).

Before you start, read [docs/architecture.md](docs/architecture.md) and check
[docs/ROADMAP.md](docs/ROADMAP.md) for what is planned.

## Development setup

Go 1.24 or newer. Python 3 is only needed for snmpsim, which the integration test uses.

```bash
git clone https://github.com/lan-dot-party/LANLeecher.git
cd LANLeecher
python3 -m venv .venv && .venv/bin/pip install snmpsim pysmi   # for the integration test
```

Run the checks that CI runs:

```bash
go vet ./...
go test ./...                        # includes the snmpsim integration test; -short skips it
go run ./tools/gendocs -check        # generated docs are up to date
golangci-lint run                    # if installed
```

Run LANLeecher locally against a simulated switch:

```bash
.venv/bin/snmpsim-command-responder --data-dir=testdata/snmpsim --agent-udpv4-endpoint=127.0.0.1:1161 &
go build -o bin/lanleecher ./cmd/lanleecher
./bin/lanleecher -c config.yaml config init
./bin/lanleecher -c config.yaml user create admin --role admin
LANLEECHER_SNMP_COMMUNITY=demo ./bin/lanleecher -c config.yaml switch add --name demo --host 127.0.0.1 --port 1161
./bin/lanleecher -c config.yaml event start "Dev LAN"
./bin/lanleecher -c config.yaml server     # http://127.0.0.1:9040
```

The community is the file name of the simulation: `demo` (24 ports with made-up guests) or
`testswitch` (the integration test fixture).

For screenshots, give the demo some history once the first poll is through: guest names
(set them in the UI or the database), then

```bash
python3 testdata/demo/seed_history.py data/lanleecher.db
```

It adds six hours of varied traffic to the running event and two earlier LAN parties. All data is
invented and the result is reproducible. The screenshots in `docs/images/` were made this way. `config.yaml`, `secret.key`, `data/` and `bin/` in the
repository root are git-ignored.

## Rules

- **English** everywhere: code, comments, docs, commit messages. UI strings go through
  `internal/web/i18n.go` with an English and a German text.
- **No private data:** no credentials, real MAC addresses, hostnames, IP addresses or paths of an
  installation – not in code, fixtures, screenshots or commit messages. Use `02:…` MACs and
  `192.0.2.0/24`.
- **Docs with the change:** API changes update `internal/api/openapi.yaml` (a test checks routes
  against it); configuration changes need doc comments in `internal/config/config.go` and
  `go run ./tools/gendocs`; data model changes need a new migration in
  `internal/store/migrations/sqlite/` – never edit a released one.
- The rules in [CLAUDE.md](CLAUDE.md#rules-that-must-not-be-broken) apply to everyone, not only
  to AI-assisted contributions: the direction mapping, deltas only, uplinks never attributed,
  hiding at query time, credentials never logged.
- Add a line to [CHANGELOG.md](CHANGELOG.md) under "Unreleased".

## Commits and pull requests

- Small, focused pull requests with a description of *why*.
- Commit messages in the imperative mood ("Add LLDP neighbors to the port list").
- CI must be green; changes to packaging run the package installation tests as well.

## Releases

Maintainers publish a release with tag `vX.Y.Z` in the GitHub web interface; the release workflow
builds, tests and attaches packages and images. See [CLAUDE.md](CLAUDE.md#releases).
