package hostfirewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUFWCoexistsWithIndependentInputChains(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "defaults")
	if err := os.WriteFile(defaults, []byte("MANAGE_BUILTINS=\"no\" # keep external chains\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user.rules"), []byte("-A ufw-user-input -p tcp --dport 8080 -j ACCEPT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	c := &Client{ConfigDir: dir, DefaultsPath: defaults, Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch name {
		case "firewall-cmd":
			return "not running", errors.New("stopped")
		case "ufw":
			return "Status: active", nil
		case "iptables":
			return "-P INPUT DROP\n-A INPUT -j ufw-before-input\n-A INPUT -j IN_BT\n-A INPUT -j f2b-sshd\n", nil
		case "iptables-save":
			return "-A ufw-user-input -p tcp --dport 8080 -j ACCEPT\n", nil
		}
		return "", nil
	}}
	s, err := c.Status(context.Background())
	if err != nil || !s.Compatible || s.Warning == "" || len(s.Rules) != 1 || !s.Rules[0].Editable || !s.Rules[0].Runtime {
		t.Fatal(s, err)
	}
	if err := c.Change(context.Background(), "ufw", s.Rules[0], true); err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		if err := c.SetEnabled(context.Background(), "ufw", active); err != nil {
			t.Fatal(err)
		}
	}
	joined := strings.Join(calls, "\n")
	for _, expected := range []string{"ufw --force delete allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8080", "ufw --force disable", "ufw --force enable"} {
		if !strings.Contains(joined, expected) {
			t.Fatal(joined)
		}
	}
	if strings.Contains(joined, "-F") || strings.Contains(joined, "-X") || strings.Contains(joined, "reset") {
		t.Fatal("unrelated rules modified", joined)
	}
}

func TestUFWRefusesDestructiveDefaultsAndUnknownConfiguration(t *testing.T) {
	for _, text := range []string{"MANAGE_BUILTINS=yes\n", "# MANAGE_BUILTINS=no\n", "MANAGE_BUILTINS=no\nMANAGE_BUILTINS=yes\n", "MANAGE_BUILTINS=$(echo no)\n"} {
		t.Run(text, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "defaults")
			_ = os.WriteFile(path, []byte(text), 0600)
			called := false
			c := &Client{DefaultsPath: path, Run: func(context.Context, string, ...string) (string, error) { called = true; return "", nil }}
			if c.SetEnabled(context.Background(), "ufw", false) == nil || called {
				t.Fatal("unsafe UFW mode change ran")
			}
			_ = os.Remove(path)
			if c.CheckUFWLifecycle() == nil {
				t.Fatal("unknown configuration allowed")
			}
		})
	}
}

func TestUFWIndependentNFTHookOnlyRestrictsLifecycle(t *testing.T) {
	calls := []string{}
	c := &Client{Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "nft" {
			return "table inet custom {\nchain INPUT { type filter hook input priority 0; policy drop; }\n}\n", nil
		}
		return "", nil
	}}
	if c.SetEnabled(context.Background(), "ufw", false) == nil {
		t.Fatal("independent nftables hook ignored")
	}
	r := Rule{Kind: "port", Protocol: "tcp", Ports: "8080", Source: "any", Destination: "any", Family: "ipv4", Direction: "in", Action: "allow"}
	if err := c.Change(context.Background(), "ufw", r, false); err != nil {
		t.Fatal("targeted UFW edit blocked", err)
	}
	if !strings.HasPrefix(calls[len(calls)-1], "ufw allow ") {
		t.Fatal(calls)
	}
}
