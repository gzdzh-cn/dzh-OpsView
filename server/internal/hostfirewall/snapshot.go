package hostfirewall

import (
	"context"
	"encoding/json"
	"strings"
)

// One local D-Bus connection avoids spawning firewall-cmd for every zone/field.
const firewalldSnapshot = `import json
from firewall.client import FirewallClient
f=FirewallClient()
rows=[]
zones=sorted(set(f.getZones()+f.config().getZoneNames()))
for permanent in (True,False):
 for zone in zones:
  z=f.config().getZoneByName(zone).getSettings() if permanent else f.getZoneSettings(zone)
  rows.append(dict(zone=zone,permanent=permanent,rich=z.getRichRules(),ports=z.getPorts(),services=z.getServices(),forward=z.getForwardPorts()))
 try:
  z=f.config().getPolicyByName('opsview-egress').getSettings() if permanent else f.getPolicySettings('opsview-egress')
  rows.append(dict(zone='opsview-egress',permanent=permanent,rich=z.getRichRules(),ports=[],services=[],forward=[],egress=True))
 except Exception: pass
print(json.dumps(dict(zones=zones,rows=rows)))
`

func (c *Client) fastFirewalldRules(ctx context.Context) ([]Rule, []string, error) {
	text, err := c.Run(ctx, "python3", "-c", firewalldSnapshot)
	if err != nil {
		return nil, nil, err
	}
	var snapshot struct {
		Zones []string `json:"zones"`
		Rows  []struct {
			Zone      string      `json:"zone"`
			Permanent bool        `json:"permanent"`
			Egress    bool        `json:"egress"`
			Rich      []string    `json:"rich"`
			Ports     [][2]string `json:"ports"`
			Services  []string    `json:"services"`
			Forward   [][4]string `json:"forward"`
		} `json:"rows"`
	}
	if err = json.Unmarshal([]byte(text), &snapshot); err != nil {
		return nil, nil, err
	}
	byKey := map[string]int{}
	rows := []Rule{}
	for _, zone := range snapshot.Rows {
		add := func(r Rule, raw string) {
			key := zone.Zone + ":" + raw
			if index, ok := byKey[key]; ok {
				if zone.Permanent {
					rows[index].Permanent = true
				} else {
					rows[index].Runtime = true
				}
				return
			}
			r.Identify("firewalld:"+zone.Zone, raw, 1)
			r.Permanent = zone.Permanent
			r.Runtime = !zone.Permanent
			byKey[key] = len(rows)
			rows = append(rows, r)
		}
		for _, raw := range zone.Rich {
			r := ParseRich(raw, zone.Zone)
			if zone.Egress {
				r.Direction = "out"
			}
			add(r, raw)
		}
		base := Rule{Kind: "port", Protocol: "tcp", Family: "dual", Source: "any", Destination: "any", Direction: "in", Action: "allow", Zone: zone.Zone, Editable: true}
		for _, p := range zone.Ports {
			r := base
			r.Ports = p[0]
			r.Protocol = p[1]
			r.Service = "@port:" + p[0] + "/" + p[1]
			add(r, "port:"+p[0]+"/"+p[1])
		}
		for _, service := range zone.Services {
			r := base
			r.Ports = service
			r.Protocol = "service"
			r.Service = service
			r.Reason = "服务附件，修改或删除影响该服务全部端口"
			add(r, "service:"+service)
		}
		for _, f := range zone.Forward {
			r := base
			r.Kind = "forward"
			r.Ports = f[0]
			r.Protocol = f[1]
			r.TargetPorts = f[2]
			r.Target = f[3]
			r.Family = "ipv4"
			if strings.Contains(r.Target, ":") {
				r.Family = "ipv6"
			}
			r.Service = "@forward:port=" + f[0] + ":proto=" + f[1] + ":toport=" + f[2] + ":toaddr=" + f[3]
			add(r, r.Service)
		}
	}
	return rows, snapshot.Zones, nil
}
