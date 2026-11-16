package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"opsview/internal/catalog"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) catalogEntry(id string) (catalog.Entry, bool) {
	if entry, ok := catalog.Find(id); ok {
		return entry, true
	}
	if !logic.Name.MatchString(id) {
		return catalog.Entry{}, false
	}
	b, err := os.ReadFile(filepath.Join(e.Root, "catalog", id+".json"))
	if err != nil {
		return catalog.Entry{}, false
	}
	var entry catalog.Entry
	err = json.Unmarshal(b, &entry)
	return entry, err == nil && catalog.Validate(entry) == nil
}
func (e *Engine) importCatalog(op model.Operation) (model.Config, error) {
	var env catalog.Envelope
	b, _ := json.Marshal(op.Params["envelope"])
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	var entry catalog.Entry
	if err := catalog.Verify(env, e.TrustDir, &entry); err != nil {
		return nil, err
	}
	if err := catalog.Validate(entry); err != nil {
		return nil, err
	}
	if _, ok := e.catalogEntry(entry.ID); ok {
		return nil, errors.New("版本目录 ID 已存在")
	}
	payload, _ := json.Marshal(entry)
	if err := security.WriteAtomic(filepath.Join(e.Root, "catalog", entry.ID+".json"), payload, 0600); err != nil {
		return nil, err
	}
	return model.Config{"entry": entry}, nil
}
func (e *Engine) plugin(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := op.Resource
	action := strings.TrimPrefix(op.Name, "plugin.")

	if action == "uninstall" {
		installed, err := e.load(r.ID)
		if err != nil {
			return nil, err
		}
		if installed.Kind != "plugin" {
			return nil, errors.New("执行器登记资源不是插件")
		}
		if _, err := os.Stat(filepath.Join(e.Root, "compose", r.ID, "compose.yaml")); err == nil {
			if _, err = e.compose(ctx, model.Operation{Name: "compose.delete", Resource: model.Resource{ID: r.ID, Kind: "compose", Name: installed.Name, Config: model.Config{}}}, log); err != nil {
				return nil, err
			}
			log("插件已卸载，业务数据与卷保留")
			return model.Config{}, nil
		}
		log("插件已卸载，配置和数据保留")
		return model.Config{}, e.forget(r.ID)
	}
	var p catalog.Plugin
	if r.Config.Bool("builtin") {
		found := false
		for _, x := range catalog.Plugins() {
			if x.ID == r.Config.String("pluginId") {
				p = x
				found = true
			}
		}
		if !found {
			return nil, errors.New("内置插件不存在")
		}
	} else {
		var env catalog.Envelope
		b, _ := json.Marshal(r.Config["envelope"])
		if err := json.Unmarshal(b, &env); err != nil {
			return nil, err
		}
		if err := catalog.Verify(env, e.TrustDir, &p); err != nil {
			return nil, err
		}
		if p.Builtin {
			return nil, errors.New("第三方插件不能声明为内置插件")
		}
	}
	if !logic.Name.MatchString(p.ID) || p.Version == "" || p.MinPanelVersion != "0.1.0" {
		return nil, errors.New("插件标识或面板兼容版本无效")
	}
	platformOK := false
	for _, platform := range p.Platforms {
		if platform == "linux/amd64" {
			platformOK = true
		}
	}
	if !platformOK {
		return nil, errors.New("插件未声明支持 Linux amd64")
	}
	for _, cap := range p.Permissions {
		if cap != "docker.compose" && cap != "backup.s3" && cap != "network.outbound" && cap != "metrics.read" {
			return nil, errors.New("插件请求了首版不支持的权限：" + cap)
		}
	}

	if action == "check" {
		stored, err := e.load(r.ID)
		if err != nil {
			return nil, err
		}
		if stored.Kind != "plugin" {
			return nil, errors.New("执行器登记资源不是插件")
		}
		if p.Type == "application" {
			return nil, errors.New("应用插件请使用 Docker 查看容器健康状态")
		}
		path := "/opt/opsview/bin/opsview-plugin-worker"
		if p.WorkerURL != "" {
			path = filepath.Join(e.Root, "plugins", r.ID, "worker")
		}
		result, err := e.runWorker(ctx, r, p, path, "check", log)
		return model.Config{"lastCheck": result}, err
	}
	if action != "install" {
		return nil, errors.New("插件操作无效")
	}
	for _, dep := range p.Dependencies {
		if dep == "docker" {
			if _, err := dockerRequest(ctx, "GET", "/info", nil); err != nil {
				return nil, errors.New("插件需要先安装 Docker")
			}
		} else {
			return nil, errors.New("不支持的插件依赖：" + dep)
		}
	}
	if p.Type == "application" {
		if !hasPermission(p, "docker.compose") {
			return nil, errors.New("应用插件未声明 docker.compose 权限")
		}
		compose := p.Compose
		values, _ := r.Config["values"].(map[string]any)
		for _, field := range p.Form {
			k, _ := field["key"].(string)
			value := fmt.Sprint(values[k])
			if values[k] == nil {
				value = fmt.Sprint(field["default"])
			}
			if strings.ContainsAny(value, "\r\n\"'{}$\\") || len(value) > 200 {
				return nil, errors.New("插件表单值无效")
			}
			if field["type"] == "port" {
				n := 0
				_, _ = fmt.Sscan(value, &n)
				if n < 1 || n > 65535 {
					return nil, errors.New("插件端口无效")
				}
			}
			compose = strings.ReplaceAll(compose, "{{"+k+"}}", value)
		}
		cr := model.Resource{ID: r.ID, Kind: "compose", Name: r.Name, Config: model.Config{"yaml": compose}}
		if _, err := e.compose(ctx, model.Operation{Name: "compose.apply", Resource: cr}, log); err != nil {
			return nil, err
		}
	} else if p.Type == "backup" || p.Type == "operations" {
		if p.WorkerURL != "" {
			path := filepath.Join(e.Root, "plugins", r.ID, "worker")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return nil, err
			}
			if err := download(ctx, p.WorkerURL, p.SHA256, path, log); err != nil {
				return nil, err
			}
			if err := os.Chmod(path, 0755); err != nil {
				return nil, err
			}
		} else if !p.Builtin {
			return nil, errors.New("运维插件必须提供经校验的 worker 可执行文件")
		}
		worker := "/opt/opsview/bin/opsview-plugin-worker"
		if p.WorkerURL != "" {
			worker = filepath.Join(e.Root, "plugins", r.ID, "worker")
		}
		if _, err := e.runWorker(ctx, r, p, worker, "health", log); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("插件类型不支持")
	}
	r.Status = "ready"
	r.Config["pluginType"] = p.Type
	r.Config["pluginId"] = p.ID
	r.Config["version"] = p.Version
	r.Config["permissions"] = p.Permissions
	return model.Config{"pluginId": p.ID, "version": p.Version, "permissions": p.Permissions}, e.save(r)
}
func hasPermission(p catalog.Plugin, s string) bool {
	for _, v := range p.Permissions {
		if v == s {
			return true
		}
	}
	return false
}
func (e *Engine) runWorker(ctx context.Context, r model.Resource, plugin catalog.Plugin, path, method string, log func(string)) (model.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	user := "ovp_" + r.ID[:min(12, len(r.ID))]
	home := filepath.Join(e.Root, "plugins", r.ID, "data")
	if err := os.MkdirAll(home, 0750); err != nil {
		return nil, err
	}
	if err := ensureUser(ctx, user, home, log); err != nil {
		return nil, err
	}
	if err := run(ctx, log, "", nil, nil, "chown", user+":"+user, home); err != nil {
		return nil, err
	}
	request, _ := json.Marshal(model.Config{"jsonrpc": "2.0", "id": 1, "method": method, "params": model.Config{"config": r.Config["values"], "protocol": "1.0", "permissions": plugin.Permissions}})
	args := []string{"--quiet", "--pipe", "--wait", "--collect", "--uid=" + user, "--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=PrivateDevices=yes", "--property=CapabilityBoundingSet=", "--property=UMask=0077", "--property=ReadWritePaths=" + home, "--property=MemoryMax=128M", "--property=TasksMax=32"}
	families := "AF_UNIX"
	if hasPermission(plugin, "network.outbound") {
		families += " AF_INET AF_INET6"
	}
	args = append(args, "--property=RestrictAddressFamilies="+families, path)
	var result bytes.Buffer
	err := run(ctx, func(s string) { result.WriteString(s + "\n") }, "", nil, bytes.NewReader(append(request, '\n')), "systemd-run", args...)
	if err != nil {
		return nil, err
	}
	var reply struct {
		JSONRPC string       `json:"jsonrpc"`
		ID      int          `json:"id"`
		Result  model.Config `json:"result"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.Unmarshal(bytes.TrimSpace(result.Bytes()), &reply); err != nil || reply.JSONRPC != "2.0" || reply.ID != 1 {
		return nil, errors.New("插件 worker 未返回有效 JSON-RPC 2.0 响应")
	}
	if reply.Error != nil {
		return nil, errors.New("插件检查失败：" + reply.Error.Message)
	}
	if reply.Result == nil {
		return nil, errors.New("插件未返回结果")
	}
	return reply.Result, nil
}

func fetchRepository(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || len(via) > 3 {
			return errors.New("仓库重定向无效")
		}
		return nil
	}}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("仓库下载失败")
	}
	return io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
}
func (e *Engine) repository(ctx context.Context, op model.Operation) (model.Config, error) {
	if err := logic.Validate(op.Resource); err != nil {
		return nil, err
	}
	data, err := fetchRepository(ctx, op.Resource.Config.String("url"))
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, errors.New("仓库索引超过限制")
	}
	var index struct {
		Plugins []catalog.Envelope `json:"plugins"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&index); err != nil {
		return nil, err
	}
	if len(index.Plugins) > 100 {
		return nil, errors.New("单仓库最多 100 个插件")
	}
	out := []model.Config{}
	seen := map[string]bool{}
	for _, builtin := range catalog.Plugins() {
		seen[builtin.ID] = true
	}
	for _, env := range index.Plugins {
		if key := op.Resource.Config.String("keyId"); key != "" && key != env.KeyID {
			return nil, errors.New("签名者不符合仓库配置")
		}
		var p catalog.Plugin
		if err = catalog.Verify(env, e.TrustDir, &p); err != nil {
			return nil, err
		}
		if p.Builtin || !logic.Name.MatchString(p.ID) || seen[p.ID] {
			return nil, errors.New("插件标识重复或试图覆盖内置插件")
		}
		seen[p.ID] = true
		b, _ := json.Marshal(p)
		var v model.Config
		_ = json.Unmarshal(b, &v)
		v["envelope"] = env
		out = append(out, v)
	}
	return model.Config{"plugins": out}, nil
}
