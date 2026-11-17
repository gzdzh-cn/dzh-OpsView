package maintenance

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMenuMapping(t *testing.T) {
	want := []string{"status", "start", "stop", "restart", "username", "password", "port", "info", "recover-access", "reset-2fa", "logs", "backup", "diagnose", "update", "ssl off"}
	if len(Commands) != 15 {
		t.Fatal("menu count")
	}
	for i, v := range want {
		if Commands[i] != v {
			t.Fatal("wrong menu mapping")
		}
	}
	for _, input := range []string{"invalid\n0\n", "99\n0\n", ""} {
		var out bytes.Buffer
		r := Runner{Out: &out, Err: &out, reader: bufio.NewReader(strings.NewReader(input))}
		if e := r.menu(); e != nil {
			t.Fatal(e)
		}
		if input != "" && !strings.Contains(out.String(), "无效编号") {
			t.Fatal("invalid menu selection not handled")
		}
	}
}
func TestPasswordPipeAndSources(t *testing.T) {
	for _, input := range []string{"short\n", strings.Repeat("x", 73), "correct-long-password\n"} {
		r := Runner{reader: bufio.NewReader(strings.NewReader(input))}
		password, e := r.password([]string{"--stdin"})
		if strings.HasPrefix(input, "correct") {
			if e != nil || password != "correct-long-password" {
				t.Fatal(e)
			}
		} else if e == nil {
			t.Fatal("accepted invalid password")
		}
	}
	for _, s := range []string{"1.2.3.4;touch /tmp/injection", "$(id)", "any", ""} {
		if sourceValid(s) {
			t.Fatal("accepted invalid source")
		}
	}
	for _, s := range []string{"1.2.3.4", "1.2.3.0/24", "2001:db8::1", "::/0"} {
		if !sourceValid(s) {
			t.Fatal("rejected valid source")
		}
	}
}
func TestNoninteractiveHelp(t *testing.T) {
	f, e := os.Open(os.DevNull)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	var out bytes.Buffer
	r := Runner{In: f, Out: &out}
	r.help()
	if !strings.Contains(out.String(), "ov <命令>") {
		t.Fatal("missing help")
	}
	if !strings.Contains(out.String(), "ssl off") || !strings.Contains(out.String(), "关闭 SSL 证书") {
		t.Fatal("SSL menu action missing from help")
	}
	if e = r.confirm("test", false); e == nil {
		t.Fatal("noninteractive confirmation bypass")
	}
	if _, e = r.password(nil); e == nil {
		t.Fatal("noninteractive password prompt")
	}
}

func TestUpdateUsesControlledScript(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	if e := os.WriteFile(filepath.Join(dir, "bash"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OV_TEST_TRACE\"\n"), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("OV_TEST_TRACE", trace)
	var out bytes.Buffer
	r := Runner{Out: &out, Err: &out}
	if e := r.command([]string{"update", "--version", "v0.1.1"}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(trace)
	if string(b) != "/opt/opsview/bin/opsview-install.sh\n--update\n--version\nv0.1.1\n" {
		t.Fatal("unsafe update arguments", string(b))
	}
	os.Remove(trace)
	if e := r.command([]string{"update", "--version", "v0.1.1;touch /tmp/injection"}); e == nil {
		t.Fatal("accepted shell syntax")
	}
	if _, e := os.Stat(trace); !os.IsNotExist(e) {
		t.Fatal("executed an invalid update")
	}
}
