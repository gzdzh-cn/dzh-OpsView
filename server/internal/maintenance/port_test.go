package maintenance

import (
	"bytes"
	"errors"
	"net"
	"opsview/internal/runtimeconfig"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPortTransaction(t *testing.T) {
	for _, scenario := range []string{"success", "failed-health", "existing-rule", "missing-source", "unknown-firewall", "unknown-nft"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			trace := filepath.Join(dir, "trace")
			config := filepath.Join(dir, "panel.json")
			write := func(name, script string) {
				t.Helper()
				if e := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0755); e != nil {
					t.Fatal(e)
				}
			}
			write("systemctl", `printf 'systemctl %s\n' "$*" >> "$OV_TEST_TRACE"
case "$*" in
 *DropInPaths*) exit 0;;
 *ExecStart*) echo '/opt/opsview/bin/opsview serve --config /etc/opsview/panel.json';;
esac
`)
			write("ufw", `printf 'ufw %s\n' "$*" >> "$OV_TEST_TRACE"
case "$*" in
 status) echo 'Status: active';;
 'show added') [ "$OV_TEST_EXISTING" != 1 ] || printf 'ufw allow proto tcp from 203.0.113.20/32 to any port %s\n' "$OV_TEST_PORT";;
esac
exit 0
`)
			if scenario == "unknown-nft" {
				write("nft", "echo 'table ip filter { chain extra { type filter hook input priority 0; } }'\n")
			} else {
				write("nft", "exit 1\n")
			}
			write("ip6tables", "echo '-A INPUT -j ufw6-before-input'\n")
			if scenario == "unknown-firewall" {
				write("iptables", "echo '-A INPUT -j custom-firewall'\n")
			} else {
				write("iptables", "echo '-A INPUT -j ufw-before-input'\n")
			}
			write("metadata", "echo '用户名：test'\n")
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			t.Setenv("OV_TEST_TRACE", trace)
			t.Setenv("SSH_CONNECTION", "")
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			t.Setenv("OV_TEST_PORT", fmtPort(port))
			if scenario == "existing-rule" {
				t.Setenv("OV_TEST_EXISTING", "1")
			}
			old := runtimeconfig.Default()
			if e = runtimeconfig.Save(config, old); e != nil {
				t.Fatal(e)
			}
			var out bytes.Buffer
			r := Runner{Out: &out, Err: &out, Executable: filepath.Join(dir, "metadata"), ConfigPath: config, CheckHealth: func(c runtimeconfig.Config) error {
				if c.Port != port {
					t.Fatal("wrong health port")
				}
				if scenario == "failed-health" {
					return errors.New("injected health failure")
				}
				return nil
			}}
			args := []string{fmtPort(port), "--yes"}
			if scenario != "missing-source" {
				args = append(args, "--source", "203.0.113.20")
			}
			e = r.port(args)
			c, _ := runtimeconfig.Load(config)
			b, _ := os.ReadFile(trace)
			commands := string(b)
			switch scenario {
			case "failed-health":
				if e == nil || c != old || !strings.Contains(commands, "ufw --force delete allow from 203.0.113.20") {
					t.Fatal("rollback failed", e, commands)
				}
			case "missing-source":
				if e == nil || c != old || strings.Contains(commands, "systemctl restart") {
					t.Fatal("source restriction bypassed")
				}
			default:
				if e != nil || c.Port != port {
					t.Fatal(e, out.String())
				}
				if strings.Contains(commands, "delete") {
					t.Fatal("removed old rules")
				}
			}
			if scenario == "existing-rule" && strings.Contains(commands, "ufw allow") {
				t.Fatal("duplicated existing rule")
			}
			if (scenario == "unknown-firewall" || scenario == "unknown-nft") && strings.Contains(commands, "ufw allow") {
				t.Fatal("mutated unknown firewall")
			}
		})
	}
}
func fmtPort(p int) string { return strconv.Itoa(p) }
