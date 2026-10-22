# Switch compatibility

LANLeecher works with any managed switch that offers, via SNMP v2c or v3:

| Needed | MIB objects | Used for |
| --- | --- | --- |
| 64-bit interface counters | `ifHCInOctets`, `ifHCOutOctets` (IF-MIB) | traffic |
| interface names and status | `ifName`, `ifAlias`, `ifDescr`, `ifType`, `ifOperStatus`, `ifHighSpeed` | port list, plausibility |
| uptime | `sysUpTime` | reboot detection |
| forwarding table | `dot1qTpFdbPort` (Q-BRIDGE-MIB) or `dot1dTpFdbPort` (BRIDGE-MIB) | MAC → port |

Optional, used when present:

| MIB objects | Used for |
| --- | --- |
| `ifCounterDiscontinuityTime` | counter reset detection without reboot |
| `dot1dBasePortIfIndex` | bridge port → ifIndex (otherwise the bridge port is taken as ifIndex) |
| `dot1qTpFdbStatus` / `dot1dTpFdbStatus` | ignore the switch's own and management MACs |
| `lldpRemSysName`, `lldpLocPortId` (LLDP-MIB) | "probably uplink" hint |
| `ipNetToPhysicalPhysAddress`, `ipNetToMediaPhysAddress` | IP addresses (switch marked as ARP source) |

SNMP v1 is not supported: it has no 64-bit counters, and a 32-bit counter wraps after about
34 seconds at 1 Gbit/s.

Use **Test connection** in the switch form: it lists every interface with status, speed and whether
64-bit counters are available.

## FDB adapters

| Adapter | Reads |
| --- | --- |
| `auto` (default) | Q-BRIDGE-MIB, falls back to BRIDGE-MIB |
| `qbridge` | `dot1qTpFdbPort` only |
| `bridge` | `dot1dTpFdbPort` only |
| `cisco-vlan` | BRIDGE-MIB once per VLAN via community `<community>@<vlan>` or v3 context `vlan-<id>` (Cisco IOS) |

## Tested models

| Vendor / model | Firmware | SNMP | Result | Notes |
| --- | --- | --- | --- | --- |
| TP-Link Omada 10-port Gigabit (8× PoE+) | – | v2c | works | see below |
| TP-Link Omada 8-port 2.5G + 2× 10G SFP+ (8× PoE+) | – | v2c | works | see below |
| TP-Link JetStream T1500G-8T | – | v2c | works | see below |

**TP-Link Omada / JetStream:**

- `ifType` is 6 (ethernetCsmacd) for every interface, including `Vlan-interface…` and
  `port-channel …`. LANLeecher therefore also classifies interfaces by name: VLAN interfaces are
  virtual, port channels are LAGs; both start as uplinks.
- BRIDGE-MIB is indexed with a VLAN prefix (`<vlan>.<mac>`), like Q-BRIDGE-MIB. LANLeecher always
  reads the MAC from the last six index parts.
- No `ifStackTable` and no IEEE8023-LAG-MIB: members of a port channel are not reported. They have
  LLDP neighbors and are marked "probably uplink" – set them to uplink.
- LLDP local port IDs use subtype interfaceName and match `ifName`.

Please report your switch: open an issue with vendor, model, firmware, SNMP version and the output of
**Test connection** (remove anything private), or a pull request for this table.
