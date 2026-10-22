# Writing an adapter

Vendor quirks and new data sources are added as adapters behind small Go interfaces, without
touching the accounting core.

## FDB source (MAC → port)

```go
// internal/fdb/fdb.go
type Source interface {
	Name() string
	Read(ctx context.Context, s Session) ([]Entry, error)
}

type Entry struct {
	MAC     macaddr.MAC
	IfIndex int // the interface the MAC was learned on
	VLAN    int // 0 if unknown
}

type Session interface {
	Client() *gosnmp.GoSNMP                                     // the switch connection
	VLAN(ctx context.Context, vlan int) (*gosnmp.GoSNMP, error) // per-VLAN view (community@vlan, v3 context vlan-<id>)
}
```

Steps:

1. Implement `Source` in `internal/fdb/<vendor>.go`. Use `snmp.WalkColumn` to walk a table column;
   it returns the parsed index of every row. Return `IfIndex` values as reported by IF-MIB – the
   poller maps them to ports.
2. Skip the switch's own MACs and multicast addresses (`mac.IsMulticast()`).
3. Register the name in `fdb.Adapters` and `fdb.ForAdapter`. It then appears in the switch form.
4. Record a real switch with `snmpsim-record-commands` (remove private data!) or write a small
   `.snmprec` fixture, and add a test.
5. Add the model to [switch-compatibility.md](switch-compatibility.md).

`cisco-vlan` in `internal/fdb/fdb.go` is a complete example that opens one connection per VLAN.

## Name probe (IP → hostname)

```go
// internal/resolver/resolver.go
type Prober interface {
	Name() string // source name stored in the history, e.g. "netbios"
	Probe(ctx context.Context, ip netip.Addr) (string, error)
}
```

Steps:

1. Implement `Prober` in `internal/resolver/`. Respect the timeout, send from an unprivileged
   port, and return `"", nil` when the client does not answer.
2. Add a case for it in `resolver.New` and its name to the `enum` tag of `Resolver.Order` in
   `internal/config/config.go`; run `go run ./tools/gendocs`.
3. If its names should rank against others, add the source to `hostname_priority`.
4. Test the packet encoding and decoding without network access (see `resolver_test.go`).

## Address source (MAC → IP)

Address sources call `Service.ObserveARP(ctx, pairs, "<source>")` with MAC/IP pairs. The service
records pairs only for MACs that were seen on a switch port and queues name probes for new pairs.
`snmp-arp` (called by the poller) and `local-neigh` (a ticker in `Service.Run`) are the examples.

## Collector

`internal/collector.Collector` abstracts counter polling (`Poll`, `System`). The SNMP collector is the
only implementation; a gNMI collector could implement the same interface. Interface classification
(`Physical`, `IsLAG`) lives there too – extend the name patterns if a vendor reports misleading
`ifType` values.

## Checklist for a pull request

- [ ] `go vet ./...` and `go test ./...` pass (with snmpsim installed for the integration test).
- [ ] No private data in fixtures (use `02:…` MACs and `192.0.2.0/24` addresses).
- [ ] Docs updated; `go run ./tools/gendocs` if the configuration changed.
- [ ] A line in `CHANGELOG.md` under "Unreleased".
