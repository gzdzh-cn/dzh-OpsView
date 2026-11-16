package runtimeconfig

import (
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func CompatibleUFW() bool {
	b, e := exec.Command("ufw", "status").Output()
	if e != nil || !strings.Contains(string(b), "Status: active") {
		return false
	}
	// An additional input hook is not an environment we can safely manage automatically.
	if b, e = exec.Command("nft", "list", "ruleset").Output(); e == nil {
		hooks := regexp.MustCompile(`(?s)chain\s+([^\s{]+)\s*\{[^{}]*?\bhook\s+input\b`)
		for _, hook := range hooks.FindAllStringSubmatch(string(b), -1) {
			if hook[1] != "INPUT" && !strings.HasPrefix(strings.ToLower(hook[1]), "ufw") {
				return false
			}
		}
		re := regexp.MustCompile(`(?s)table\s+(?:inet|ip|ip6)\s+([^\s{]+)\s*\{(.*?)\n\}`)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if strings.Contains(m[2], "hook input") && !strings.Contains(strings.ToLower(m[1]), "ufw") && m[1] != "filter" {
				return false
			}
		}
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		b, e = exec.Command(tool, "-S", "INPUT").Output()
		if e == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "-A INPUT ") && !strings.Contains(line, "ufw-") && !strings.Contains(line, "ufw6-") {
					return false
				}
			}
		}
	}
	return true
}

func normalizedSource(source string) string {
	if ip := net.ParseIP(source); ip != nil {
		if ip.To4() != nil {
			return ip.String() + "/32"
		}
		return ip.String() + "/128"
	}
	if _, network, e := net.ParseCIDR(source); e == nil {
		return network.String()
	}
	return source
}

// UFW normalizes argument order and host CIDRs in 'show added'. Compare meaning,
// including proto/from/to, rather than the order originally supplied by the user.
func ExistingUFWRule(output, source string, port int) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "ufw" || fields[1] != "allow" {
			continue
		}
		foundSource, foundPort, proto, to := "", "", "", ""
		for i := 2; i < len(fields); i++ {
			if fields[i] == "comment" {
				break
			}
			if i+1 < len(fields) {
				switch fields[i] {
				case "from":
					foundSource = fields[i+1]
				case "port":
					foundPort = fields[i+1]
				case "proto":
					proto = fields[i+1]
				case "to":
					to = fields[i+1]
				}
			}
		}
		if normalizedSource(foundSource) == normalizedSource(source) && foundPort == strconv.Itoa(port) && proto == "tcp" && to == "any" {
			return true
		}
	}
	return false
}
