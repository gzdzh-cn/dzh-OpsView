package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"github.com/gogf/gf/v2/net/ghttp"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"opsview/internal/account"
	"opsview/internal/catalog"
	"opsview/internal/controller"
	"opsview/internal/dao"
	"opsview/internal/executor"
	"opsview/internal/logic"
	"opsview/internal/maintenance"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"opsview/internal/version"
	"opsview/public"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if filepath.Base(os.Args[0]) == "ov" {
		if err := maintenance.Run(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "OpsView:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(version.Current)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "runtime-config" {
		if err := maintenance.Runtime(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "OpsView:", err)
			os.Exit(1)
		}
		return
	}

	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	dev := flags.Bool("dev", false, "开发模式，仅允许监听本机 HTTP")
	addr := flags.String("listen", "127.0.0.1:18790", "面板监听地址")
	defaultData := ".opsview"
	executable, _ := os.Executable()
	executable, _ = filepath.EvalSymlinks(executable)
	installed := filepath.Dir(executable) == "/opt/opsview/bin"
	if installed {
		defaultData = "/var/lib/opsview/panel"
	}
	data := flags.String("data", defaultData, "面板数据目录")
	outputPath := flags.String("output", "", "恢复到尚不存在的新目录")
	root := flags.String("root", "/var/lib/opsview/managed", "业务数据根目录")
	socket := flags.String("socket", "/run/opsview/executor.sock", "执行器 Socket")
	web := flags.String("web", "", "开发时的静态资源目录")
	trustID := flags.String("id", "", "签名者标识")
	file := flags.String("file", "", "公钥文件")
	trustDir := flags.String("trust-dir", "/etc/opsview/trust.d", "信任公钥目录")
	configPath := flags.String("config", "", "运行配置文件（root 管理）")
	_ = flags.Parse(args)
	explicitListen := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			explicitListen = true
		}
	})
	if installed && command == "serve" && !*dev && *configPath == "" && !explicitListen {
		*configPath = runtimeconfig.Path
	}
	if *configPath != "" {
		cfg, err := runtimeconfig.Load(*configPath)
		if err != nil {
			log.Fatal(err)
		}
		if explicitListen {
			log.Fatal("--config 与 --listen 不能同时使用")
		}
		*addr = cfg.Address()
	}

	var err error
	*data, err = filepath.Abs(*data)
	if err != nil {
		log.Fatal(err)
	}
	if command == "trust-key" {
		if !logic.Name.MatchString(*trustID) || strings.Contains(*trustID, ".") {
			log.Fatal("id 只允许字母、数字、横线、下划线")
		}
		key, err := os.ReadFile(*file)
		if err != nil {
			log.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(key)))
		if err != nil || len(decoded) != 32 {
			log.Fatal("公钥须为 Base64 编码的 Ed25519 公钥")
		}
		if err = security.WriteAtomic(filepath.Join(*trustDir, *trustID+".pub"), key, 0644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("已信任签名者：", *trustID)
		return
	}

	if installed && os.Geteuid() == 0 {
		child := exec.Command("runuser", append([]string{"-u", "opsview", "--", executable}, os.Args[1:]...)...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := child.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if command == "restore-config" {
		if *file == "" || *outputPath == "" {
			log.Fatal("用 --file 指定备份，--output 指定新目录；当前 --data 目录必须保留原 master.key")
		}
		if err := logic.RestorePanel(*data, *file, *outputPath); err != nil {
			log.Fatal(err)
		}
		fmt.Println("已恢复到新目录，请验证后停止面板并切换数据目录：", *outputPath)
		return
	}
	store, err := dao.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer store.DB.Close()
	if command == "admin-init" {
		username, password, e := account.RandomCredentials()
		if e != nil {
			log.Fatal(e)
		}
		if e = account.Initialize(store, username, password, "local"); e != nil {
			log.Fatal(e)
		}
		_ = os.Remove(filepath.Join(*data, "setup.token"))
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"username": username, "password": password})
		return
	}
	if command == "admin-info" {
		var a account.Admin
		if err = store.Setting("admin", &a); errors.Is(err, sql.ErrNoRows) {
			fmt.Println("尚未初始化管理员；可用 ov init 生成随机账号。")
			return
		} else if err != nil {
			log.Fatal(err)
		}
		fmt.Println("用户名：" + a.Username)
		return
	}
	if command == "admin-change" {
		var request struct {
			Username  string `json:"username"`
			Password  string `json:"password"`
			ResetTOTP bool   `json:"resetTOTP"`
		}
		if err = json.NewDecoder(io.LimitReader(os.Stdin, 4096)).Decode(&request); err != nil {
			log.Fatal("无法读取维护请求")
		}
		if request.Username == "" && request.Password == "" && !request.ResetTOTP {
			log.Fatal("空的账号变更")
		}
		if err = account.Change(store, request.Username, request.Password, request.ResetTOTP, nil, "local"); err != nil {
			log.Fatal(err)
		}
		return
	}
	if command == "reset-admin" {
		password := os.Getenv("OPSVIEW_NEW_PASSWORD")
		if password == "" {
			_, password, err = account.RandomCredentials()
			if err != nil {
				log.Fatal(err)
			}
		}
		if err = account.Change(store, "", password, true, nil, "local"); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("管理员密码已重置，双因素认证已关闭。新密码：%s\n", password)
		return
	}

	if command == "recover-access" {
		var panel model.Config
		_ = store.Setting("panel", &panel)
		if panel == nil {
			panel = model.Config{}
		}
		panel["allowedCIDRs"] = ""
		if err = store.Set("panel", panel); err != nil {
			log.Fatal(err)
		}
		_, _ = store.DB.Exec("DELETE FROM sessions")
		store.Audit("maintenance.recover-access", "", "local")
		fmt.Println("已解除面板 IP 限制，所有旧会话已失效")
		return
	}
	if command == "backup-config" {
		app := &logic.App{Store: store, PanelDir: *data}
		result, err := app.BackupPanel(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(filepath.Join(*data, "backups", result.String("metadataBackup")))
		return
	}
	if command == "diagnose" {
		tasks, _ := store.Tasks()
		rs, _ := store.Resources("")
		output := map[string]any{"version": version.Current, "resources": len(rs), "tasks": tasks, "executorSocket": *socket}
		b, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(b))
		return
	}
	if command != "serve" {
		log.Fatal("命令支持 serve、reset-admin、recover-access、backup-config、restore-config、diagnose、trust-key")
	}
	if *dev {
		host, _, err := net.SplitHostPort(*addr)
		if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
			log.Fatal("开发模式只能监听本机地址")
		}
	}
	var initial map[string]any
	if err = store.Setting("admin", &initial); errors.Is(err, sql.ErrNoRows) {
		tokenPath := filepath.Join(*data, "setup.token")
		token, e := os.ReadFile(tokenPath)
		if os.IsNotExist(e) {
			token = []byte(security.ID() + security.ID())
			if err = security.WriteAtomic(tokenPath, token, 0600); err != nil {
				log.Fatal(err)
			}
		} else if e != nil {
			log.Fatal(e)
		}
		log.Printf("尚未初始化管理员，请读取本机令牌文件 %s 或运行 ov init", tokenPath)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Fatal(err)
	}
	var imported []catalog.Entry
	if store.Setting("catalog-imports", &imported) == nil {
		for _, entry := range imported {
			_ = catalog.Register(entry)
		}
	}
	app := &logic.App{Store: store, Executor: executor.NewClient(*socket), Root: *root, Development: *dev, PanelConfigPath: *configPath}
	app.Start()
	defer app.Stop()
	var assets fs.FS = public.Files()
	if *web != "" {
		assets = os.DirFS(*web)
	}
	ctrl := controller.New(app, *data, assets)
	handler := ctrl.Handler()
	server := ghttp.GetServer("opsview")
	server.SetAddr(*addr)
	server.SetDumpRouterMap(false)
	server.SetReadTimeout(30 * time.Second)
	server.SetIdleTimeout(90 * time.Second)
	tlsEnabled := !*dev
	if *configPath != "" {
		cfg, er := runtimeconfig.Load(*configPath)
		if er != nil {
			log.Fatal(er)
		}
		tlsEnabled = tlsEnabled && cfg.Scheme() == "https"
	}
	if tlsEnabled {
		certPath := filepath.Join(*data, "panel.pem")
		if _, err = os.Stat(certPath); os.IsNotExist(err) {
			if err = createCertificate(certPath); err != nil {
				log.Fatal(err)
			}
		}
		server.EnableHTTPS(certPath, certPath, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			b, err := os.ReadFile(certPath)
			if err != nil {
				return nil, err
			}
			pair, err := tls.X509KeyPair(b, b)
			return &pair, err
		}})
	}
	serve := func(r *ghttp.Request) { handler.ServeHTTP(r.Response.BufferWriter.RawWriter(), r.Request) }
	server.BindHandler("/*path", serve)
	server.BindHandler("/", serve)
	if err = server.Start(); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	_ = server.Shutdown()
}
func createCertificate(path string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "OpsView local panel"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost", hostname}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return err
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	b = append(b, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})...)
	return security.WriteAtomic(path, b, 0600)
}
