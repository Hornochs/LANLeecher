package collect

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

func mac(s string) macaddr.MAC {
	m, err := macaddr.Parse(s)
	if err != nil {
		panic(err)
	}
	return m
}

func ips(s ...string) []netip.Addr {
	var out []netip.Addr
	for _, x := range s {
		out = append(out, netip.MustParseAddr(x))
	}
	return out
}

func stubAddrs(ifname string) []netip.Addr {
	if ifname == "eno1" || ifname == "vmbr0" {
		return ips("192.0.2.10", "fe80::1", "169.254.1.1", "::ffff:192.0.2.11", "127.0.0.1",
			"224.0.0.1", "0.0.0.0", "2001:db8::10", "192.0.2.10")
	}
	return nil
}

func opts(root string) Options {
	return Options{Root: root, Addrs: stubAddrs}
}

func TestCollect(t *testing.T) {
	tests := []struct {
		name string
		mode string
		opt  Options
		want []Interface
	}{
		{
			name: "linux",
			mode: "linux",
			opt:  opts("../../../testdata/agent/linux"),
			want: []Interface{
				{MAC: mac("bc:24:11:00:00:02"), IfName: "bond0", Kind: "host", Down: 4000, Up: 3000},
				{MAC: mac("bc:24:11:00:00:01"), IfName: "eno1", Kind: "host", Down: 1000, Up: 500,
					IPs: ips("192.0.2.10", "192.0.2.11", "2001:db8::10")},
				{MAC: mac("02:aa:bb:cc:dd:01"), IfName: "wlan0", Kind: "host", Down: 300, Up: 200},
			},
		},
		{
			name: "proxmox via auto",
			mode: "auto",
			opt:  opts("../../../testdata/agent/proxmox"),
			want: []Interface{
				{MAC: mac("bc:24:11:12:34:56"), IfName: "tap101i0", Kind: "vm", Name: "web01", VMID: 101,
					VLAN: 20, Down: 200, Up: 100},
				{MAC: mac("bc:24:11:ab:cd:ef"), IfName: "veth200i0", Kind: "ct", Name: "ct-app", VMID: 200,
					Down: 400, Up: 300, IPs: ips("192.0.2.5", "2001:db8::5")},
				{MAC: mac("bc:24:11:00:00:01"), IfName: "vmbr0", Kind: "host", Down: 8800, Up: 7800,
					IPs: ips("192.0.2.10", "192.0.2.11", "2001:db8::10")},
			},
		},
		{
			name: "proxmox exclude guests",
			mode: "proxmox",
			opt: Options{Root: "../../../testdata/agent/proxmox", Addrs: stubAddrs,
				Include: []string{"*"}, Exclude: []string{"tap*", "veth*"}},
			want: []Interface{
				{MAC: mac("bc:24:11:00:00:01"), IfName: "vmbr0", Kind: "host", Down: 8800, Up: 7800,
					IPs: ips("192.0.2.10", "192.0.2.11", "2001:db8::10")},
			},
		},
		{
			name: "include only a guest",
			mode: "proxmox",
			opt: Options{Root: "../../../testdata/agent/proxmox", Addrs: stubAddrs,
				Include: []string{"veth200i*"}},
			want: []Interface{
				{MAC: mac("bc:24:11:ab:cd:ef"), IfName: "veth200i0", Kind: "ct", Name: "ct-app", VMID: 200,
					Down: 400, Up: 300, IPs: ips("192.0.2.5", "2001:db8::5")},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Collect(tc.mode, tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestDetectAndErrors(t *testing.T) {
	if got := Detect(Options{Root: "../../../testdata/agent/proxmox"}); got != "proxmox" {
		t.Errorf("proxmox fixture detected as %q", got)
	}
	if got := Detect(Options{Root: "../../../testdata/agent/linux"}); got != "linux" {
		t.Errorf("linux fixture detected as %q", got)
	}
	if _, err := Collect("bogus", Options{}); err == nil {
		t.Error("unknown mode must fail")
	}
	if _, err := Linux(Options{Root: t.TempDir()}); err == nil {
		t.Error("missing sysfs must fail")
	}
}

func TestFilterIPsLimit(t *testing.T) {
	var in []netip.Addr
	for i := 1; i <= 12; i++ {
		in = append(in, netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}))
	}
	if got := filterIPs(in); len(got) != maxIPs {
		t.Errorf("got %d IPs, want %d", len(got), maxIPs)
	}
}

func TestDirectionMapping(t *testing.T) {
	if d, u := hostDirection(10, 20); d != 10 || u != 20 {
		t.Errorf("host: rx must be Down, tx Up; got %d/%d", d, u)
	}
	if d, u := guestDirection(10, 20); d != 20 || u != 10 {
		t.Errorf("guest: tx must be Down, rx Up; got %d/%d", d, u)
	}
}

func TestParseGuestConf(t *testing.T) {
	conf := "name: a\nnet0: virtio=BC:24:11:00:00:01\nnet1: bogus\nnet2: macaddr=bc:24:11:00:00:02,tag=99999,model=x\nnetx: virtio=bc:24:11:00:00:03\n[snap]\nnet3: virtio=bc:24:11:00:00:04\n"
	name, nics := parseGuestConf(conf, "name", true)
	if name != "a" || len(nics) != 2 || nics[0].index != 0 || nics[1].index != 2 || nics[1].vlan != 0 {
		t.Errorf("unexpected parse: %q %+v", name, nics)
	}
}
