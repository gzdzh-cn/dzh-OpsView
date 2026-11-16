package main

import (
	"context"
	"flag"
	"log"
	"opsview/internal/executor"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
)

func main() {
	root := flag.String("root", "/var/lib/opsview/managed", "受管理业务数据目录")
	socket := flag.String("socket", "/run/opsview/executor.sock", "Unix Socket 路径")
	trust := flag.String("trust-dir", "/etc/opsview/trust.d", "受信任公钥目录")
	group := flag.String("group", "opsview", "允许连接的系统组")
	flag.Parse()
	g, err := user.LookupGroup(*group)
	if err != nil {
		log.Fatal(err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	engine := &executor.Engine{Root: *root, TrustDir: *trust, SocketGID: gid}
	log.Printf("OpsView executor listening on %s", *socket)
	if err = engine.Serve(ctx, *socket); err != nil {
		log.Fatal(err)
	}
}
