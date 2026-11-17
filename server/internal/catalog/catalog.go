package catalog

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

//go:embed versions.json
var data []byte

//go:embed plugins.json
var pluginData []byte

type BinaryArtifact struct {
	RuntimePackages []string `json:"runtimePackages,omitempty"`
	System          string   `json:"system"` // ubuntu:24.04, etc.; cross-distro artifacts are rejected.
	Source          string   `json:"source"`
	SHA256          string   `json:"sha256"`
	BuildPrefix     string   `json:"buildPrefix"`
}
type Entry struct {
	VerifiedSystems map[string][]string `json:"verifiedSystems,omitempty"`
	Availability    map[string]string   `json:"availability,omitempty"`
	Recommended     bool                `json:"recommended,omitempty"`
	Binaries        []BinaryArtifact    `json:"binaries,omitempty"`
	DisabledReason  string              `json:"disabledReason,omitempty"`
	RecipeRevision  string              `json:"recipeRevision,omitempty"`
	Methods         []string            `json:"methods,omitempty"`
	ID              string              `json:"id"`
	Software        string              `json:"software"`
	Version         string              `json:"version"`
	Source          string              `json:"source,omitempty"`
	SHA256          string              `json:"sha256,omitempty"`
	Image           string              `json:"image,omitempty"`
	Platforms       []string            `json:"platforms"`
	Description     string              `json:"description"`
}
type Plugin struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Version         string           `json:"version"`
	Description     string           `json:"description"`
	Type            string           `json:"type"`
	MinPanelVersion string           `json:"minPanelVersion"`
	Platforms       []string         `json:"platforms"`
	Permissions     []string         `json:"permissions"`
	Dependencies    []string         `json:"dependencies"`
	Compose         string           `json:"compose,omitempty"`
	WorkerURL       string           `json:"workerUrl,omitempty"`
	SHA256          string           `json:"sha256,omitempty"`
	Form            []map[string]any `json:"form"`
	Builtin         bool             `json:"builtin"`
}

var mu sync.RWMutex
var entries []Entry

func init() {
	if err := json.Unmarshal(data, &entries); err != nil {
		panic(err)
	}
}
func Entries() []Entry { mu.RLock(); defer mu.RUnlock(); return append([]Entry{}, entries...) }
func Find(id string) (Entry, bool) {
	for _, e := range Entries() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}
func Register(e Entry) error {
	if err := Validate(e); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	for _, old := range entries {
		if old.ID == e.ID {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(e)
			if string(a) != string(b) {
				return errors.New("禁止覆盖已有软件目录条目，请使用新的 ID")
			}
			return nil
		}
	}
	entries = append(entries, e)
	return nil
}
func Plugins() []Plugin { var p []Plugin; _ = json.Unmarshal(pluginData, &p); return p }
func Validate(e Entry) error {
	if e.DisabledReason != "" {
		return errors.New(e.DisabledReason)
	}
	platformOK := false
	for _, platform := range e.Platforms {
		if platform == "linux/amd64" {
			platformOK = true
		}
	}
	if !platformOK {
		return errors.New("配方必须声明支持 linux/amd64")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`).MatchString(e.ID) || e.Version == "" {
		return errors.New("配方 ID 或版本无效")
	}
	if e.Software != "nginx" && e.Software != "mysql" && e.Software != "postgresql" && e.Software != "nodejs" && e.Software != "php" && e.Software != "redis" {
		return errors.New("配方只能选择已实现的核心软件安装器")
	}
	if e.Source != "" {
		u, err := url.Parse(e.Source)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
			return errors.New("配方下载源须使用 HTTPS")
		}
		if b, err := hex.DecodeString(e.SHA256); err != nil || len(b) != 32 {
			return errors.New("SHA-256 无效")
		}
	}
	if e.Image != "" {
		parts := strings.Split(e.Image, "@sha256:")
		if len(parts) != 2 {
			return errors.New("镜像必须固定摘要")
		}
		if b, err := hex.DecodeString(parts[1]); err != nil || len(b) != 32 {
			return errors.New("镜像摘要无效")
		}
	}
	for method, systems := range e.VerifiedSystems {
		if method != "source" && method != "binary" {
			return errors.New("验收安装方式无效")
		}
		for _, system := range systems {
			if !targetSystems[system] {
				return errors.New("验收系统无效")
			}
		}
	}
	for _, method := range e.Methods {
		if method != "source" && method != "binary" {
			return errors.New("原生安装方式无效")
		}
	}
	for _, artifact := range e.Binaries {
		for _, pkg := range artifact.RuntimePackages {
			if !regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(?::amd64)?$`).MatchString(pkg) {
				return errors.New("运行依赖包名称无效")
			}
		}
		switch artifact.System {
		case "ubuntu:22.04", "ubuntu:24.04", "debian:12", "debian:13":
		default:
			return errors.New("预编译包系统不受支持")
		}
		u, err := url.Parse(artifact.Source)
		digest, digestErr := hex.DecodeString(artifact.SHA256)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || digestErr != nil || len(digest) != 32 {
			return errors.New("预编译包来源或摘要无效")
		}
		if !strings.HasPrefix(artifact.BuildPrefix, "/var/lib/opsview/") || filepath.Clean(artifact.BuildPrefix) != artifact.BuildPrefix || !strings.HasSuffix(artifact.BuildPrefix, "/runtime") {
			return errors.New("预编译包构建前缀无效")
		}
	}
	if e.Source == "" && e.Image == "" {
		return errors.New("至少提供一种安装来源")
	}
	return nil
}

type Envelope struct {
	KeyID     string `json:"keyId"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func Verify(env Envelope, trust string, out any) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(env.KeyID) {
		return errors.New("签名密钥标识无效")
	}
	keyText, err := os.ReadFile(filepath.Join(trust, env.KeyID+".pub"))
	if err != nil {
		return errors.New("签名者未受信任，请通过主机命令导入公钥")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyText)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("信任公钥无效")
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil || len(payload) > 2<<20 {
		return errors.New("插件清单编码无效或超过限制")
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil || !ed25519.Verify(key, payload, sig) {
		return errors.New("Ed25519 签名校验失败")
	}
	d := json.NewDecoder(strings.NewReader(string(payload)))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
