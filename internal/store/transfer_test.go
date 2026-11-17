package store_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/store"
	"github.com/lan-dot-party/lanleecher/internal/transfer"
)

// seedTransfer creates a switch with three ports, two MACs on a client port,
// two MACs sharing a hypervisor port (port group) and unassigned traffic on
// a third port, all in one ended event.
func seedTransfer(t *testing.T, db *store.DB, swName, host string) {
	t.Helper()
	ctx := context.Background()
	sw := &store.Switch{Name: swName, Host: host, Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte("secret-blob"),
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ev, err := db.StartEvent(ctx, "Party 2026", "notes")
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(ctx, func(tx *store.Tx) error {
		ports, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
			{IfIndex: 1, IfName: "Gi1/0/1", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
			{IfIndex: 2, IfName: "Gi1/0/2", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
			{IfIndex: 3, IfName: "Gi1/0/3", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
			{IfIndex: 24, IfName: "Gi1/0/24", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "uplink"},
		}, at)
		if err != nil {
			return err
		}
		if err := tx.RecordSightings(ctx, sw.ID, []store.Sighting{
			{MAC: "02:00:00:00:00:01", PortID: ports[1].ID, VLAN: 1},
			{MAC: "02:00:00:00:00:02", PortID: ports[2].ID, VLAN: 1},
			{MAC: "02:00:00:00:00:03", PortID: ports[2].ID, VLAN: 1},
		}, nil, 8, at); err != nil {
			return err
		}
		for i := 0; i < 3; i++ {
			ts := at.Add(time.Duration(i) * 5 * time.Minute)
			if err := tx.AddPortTraffic(ctx, ev.ID, ports[1].ID, 1000, 100, ts); err != nil {
				return err
			}
			if err := tx.AddSubjectTraffic(ctx, ev.ID, store.SubjectMAC, "02:00:00:00:00:01", ports[1].ID, 1000, 100, ts); err != nil {
				return err
			}
			gid, err := tx.EnsurePortGroup(ctx, ev.ID, ports[2].ID)
			if err != nil {
				return err
			}
			for _, m := range []string{"02:00:00:00:00:02", "02:00:00:00:00:03"} {
				if err := tx.TouchPortGroupMAC(ctx, gid, m, ts); err != nil {
					return err
				}
			}
			if err := tx.AddPortTraffic(ctx, ev.ID, ports[2].ID, 5000, 500, ts); err != nil {
				return err
			}
			if err := tx.AddSubjectTraffic(ctx, ev.ID, store.SubjectPortGroup, itoa(gid), ports[2].ID, 5000, 500, ts); err != nil {
				return err
			}
			if err := tx.AddPortTraffic(ctx, ev.ID, ports[3].ID, 7, 7, ts); err != nil {
				return err
			}
			if err := tx.AddSubjectTraffic(ctx, ev.ID, store.SubjectUnassigned, itoa(ports[3].ID), ports[3].ID, 7, 7, ts); err != nil {
				return err
			}
			if err := tx.AddPortTraffic(ctx, ev.ID, ports[24].ID, 99999, 99999, ts); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordHostname(ctx, "02:00:00:00:00:01", "gamer-pc", "dhcp", at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordIP(ctx, "02:00:00:00:00:01", "192.0.2.5", "snmp-arp", at); err != nil {
		t.Fatal(err)
	}
	dev, err := db.DeviceOfMAC(ctx, "02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDevice(ctx, dev, "Gamer", false, "front row"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.StopEvent(ctx); err != nil {
		t.Fatal(err)
	}
	link := &store.WANLink{Name: "Fiber", DownMbps: 1000}
	if err := db.CreateWANLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	ports, err := db.ListPorts(ctx, sw.ID)
	if err != nil {
		t.Fatal(err)
	}
	wan := accounting.RoleWAN
	for _, p := range ports {
		if p.IfIndex == 24 {
			if err := db.UpdatePorts(ctx, []int64{p.ID}, store.PortSettings{Role: &wan, WANLinkID: &link.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func mustEvent(t *testing.T, db *store.DB, slug string) int64 {
	t.Helper()
	ev, err := db.EventBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return ev.ID
}

func roundtrip(t *testing.T, doc *transfer.Doc) *transfer.Doc {
	t.Helper()
	var buf bytes.Buffer
	if err := transfer.Write(&buf, doc); err != nil {
		t.Fatal(err)
	}
	out, err := transfer.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func rankingSummary(t *testing.T, db *store.DB, slug string) map[string][2]int64 {
	t.Helper()
	ctx := context.Background()
	ev, err := db.EventBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := db.Rankings(ctx, store.RankingQuery{EventID: ev.ID, Order: "down", IncludeHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]int64{}
	for _, e := range entries {
		key := e.Type + ":" + e.Name + ":" + strings.Join(e.MACs, ",")
		out[key] = [2]int64{e.BytesDown, e.BytesUp}
	}
	return out
}

func TestExportImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	src := openDB(t)
	seedTransfer(t, src, "sw1", "192.0.2.10")
	doc, stats, err := src.Export(ctx, store.ExportOptions{Software: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SkippedRows != 0 {
		t.Fatalf("skipped %d rows", stats.SkippedRows)
	}
	doc = roundtrip(t, doc)

	dst := openDB(t)
	rep, err := dst.Import(ctx, doc, store.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Switches) != 1 || rep.Switches[0].Action != store.ActionArchived || rep.Switches[0].PortsNew != 4 {
		t.Fatalf("switches: %+v", rep.Switches)
	}
	if rep.MACsNew != 3 || rep.MACsMerged != 0 || len(rep.Events) != 1 || rep.Events[0].Action != store.ActionImported {
		t.Fatalf("report: %+v", rep)
	}

	want := rankingSummary(t, src, "party-2026")
	got := rankingSummary(t, dst, "party-2026")
	if len(want) != 2 || len(got) != len(want) { // unassigned traffic is not ranked
		t.Fatalf("rankings differ in size: want %v, got %v", want, got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: want %v, got %v", k, v, got[k])
		}
	}

	// The archived switch has no credentials and is not polled.
	sws, err := dst.ListSwitches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sws) != 1 || !sws[0].Archived || sws[0].Enabled || len(sws[0].CredentialsEnc) != 0 {
		t.Fatalf("archived switch: %+v", sws[0])
	}

	// Port totals (including the uplink) and the device name survive.
	ev, _ := dst.EventBySlug(ctx, "party-2026")
	if ev.Active() {
		t.Fatal("imported event must not be active")
	}
	pt, err := dst.PortTrafficBySwitch(ctx, ev.ID, sws[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var uplink int64
	for _, v := range pt {
		uplink = max(uplink, v.BytesDown)
	}
	if uplink != 3*99999 {
		t.Fatalf("uplink total %d", uplink)
	}

	// The WAN link and its traffic (with buckets) come along.
	wantWAN, err := src.WANTraffic(ctx, mustEvent(t, src, "party-2026"), true)
	if err != nil {
		t.Fatal(err)
	}
	gotWAN, err := dst.WANTraffic(ctx, ev.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotWAN) != 1 || gotWAN[0].Link.Name != "Fiber" || gotWAN[0].Link.DownMbps != 1000 ||
		gotWAN[0].InternetDown != wantWAN[0].InternetDown || gotWAN[0].InternetDown != 3*99999 ||
		len(gotWAN[0].Series) != len(wantWAN[0].Series) || len(gotWAN[0].Series) != 3 {
		t.Fatalf("WAN after import: %+v, want %+v", gotWAN, wantWAN)
	}
	if rep.WANLinksNew != 1 {
		t.Fatalf("WAN links created: %d", rep.WANLinksNew)
	}

	// Exporting the import again gives the same content.
	doc2, _, err := dst.Export(ctx, store.ExportOptions{Software: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc2.Events[0].Buckets) != len(doc.Events[0].Buckets) || len(doc2.MACs) != 3 ||
		len(doc2.MACs[0].Hostnames) != 1 || doc2.Devices[0].DisplayName == "" {
		t.Fatalf("re-export differs: %+v", doc2)
	}
	var unassigned int64
	for _, tr := range doc2.Events[0].Totals {
		if tr.Type == transfer.SubjectUnassigned && tr.Port.IfIndex == 3 {
			unassigned += tr.Down
		}
	}
	if unassigned != 21 {
		t.Fatalf("unassigned traffic %d, want 21", unassigned)
	}
}

func TestImportConflicts(t *testing.T) {
	ctx := context.Background()
	src := openDB(t)
	seedTransfer(t, src, "sw1", "192.0.2.10")
	doc, _, err := src.Export(ctx, store.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// The target already knows sw1 (same host), a MAC and the slug.
	dst := openDB(t)
	seedTransfer(t, dst, "sw1", "192.0.2.10")
	before := rankingSummary(t, dst, "party-2026")

	dry, err := dst.Import(ctx, doc, store.ImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.Switches[0].Action != store.ActionMapped || dry.MACsMerged != 3 || dry.MACsNew != 0 ||
		dry.Events[0].Action != store.ActionRenamed || dry.Events[0].TargetSlug != "party-2026-import" {
		t.Fatalf("dry run: %+v", dry)
	}
	if _, err := dst.EventBySlug(ctx, "party-2026-import"); err == nil {
		t.Fatal("dry run wrote data")
	}

	skip, err := dst.Import(ctx, doc, store.ImportOptions{EventConflict: store.ConflictSkip})
	if err != nil {
		t.Fatal(err)
	}
	if skip.Events[0].Action != store.ActionSkipped {
		t.Fatalf("skip: %+v", skip.Events)
	}
	rep, err := dst.Import(ctx, doc, store.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events[0].TargetSlug != "party-2026-import" {
		t.Fatalf("renamed: %+v", rep.Events)
	}
	// The existing event is untouched; the imported one has the same numbers.
	after := rankingSummary(t, dst, "party-2026")
	imported := rankingSummary(t, dst, "party-2026-import")
	for k, v := range before {
		if after[k] != v || imported[k] != v {
			t.Errorf("%s: before %v after %v imported %v", k, v, after[k], imported[k])
		}
	}

	// Same name, different host: archived under a new name.
	other := openDB(t)
	seedTransfer(t, other, "sw1", "192.0.2.99")
	rep, err = other.Import(ctx, doc, store.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Switches[0].Action != store.ActionRenamed || rep.Switches[0].TargetName != "sw1 (import)" {
		t.Fatalf("renamed switch: %+v", rep.Switches)
	}
}

func TestExportEventFilter(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	seedTransfer(t, db, "sw1", "192.0.2.10")
	if _, _, err := db.Export(ctx, store.ExportOptions{Events: []string{"nope"}}); err == nil {
		t.Fatal("unknown event must fail")
	}
	doc, _, err := db.Export(ctx, store.ExportOptions{Events: []string{"party-2026"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Events) != 1 || len(doc.MACs) != 3 {
		t.Fatalf("filtered export: %d events, %d MACs", len(doc.Events), len(doc.MACs))
	}
}
