package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"opsview/internal/security"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Path = "/etc/opsview/panel.json"

type Config struct {
	AllowedCIDRs *string `json:"allowedCIDRs,omitempty"`
	Protocol     string  `json:"protocol,omitempty"`
	Entry        string  `json:"entry,omitempty"`
	AllowedHost  string  `json:"allowedHost,omitempty"`
	ListenHost   string  `json:"listenHost"`
	Port         int     `json:"port"`
	PublicHost   string  `json:"publicHost,omitempty"`
}

func Default() Config { return Config{Protocol: "https", ListenHost: "0.0.0.0", Port: 18790} }
func ValidHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsPrivate()
	}
	if len(host) > 253 || !strings.Contains(host, ".") || regexp.MustCompile(`^[0-9.]+$`).MatchString(host) {
		return false
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	for _, label := range strings.Split(host, ".") {
		if !re.MatchString(label) {
			return false
		}
	}
	return true
}
func (c Config) Validate() error {
	if c.AllowedCIDRs != nil {
		for _, item := range strings.Fields(*c.AllowedCIDRs) {
			if _, _, err := net.ParseCIDR(item); err != nil {
				return errors.New("IP 限制必须使用 CIDR 格式")
			}
		}
	}
	if c.Protocol != "" && c.Protocol != "https" && c.Protocol != "http" {
		return errors.New("协议必须为 http 或 https")
	}
	if c.Entry != "" && !regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`).MatchString(c.Entry) {
		return errors.New("安全入口须为 8–64 位字母数字、横线或下划线")
	}
	if c.AllowedHost != "" && !ValidHost(c.AllowedHost) {
		return errors.New("访问域名无效")
	}

	if net.ParseIP(c.ListenHost) == nil || c.Port < 1 || c.Port > 65535 {
		return errors.New("监听地址必须是 IP，端口须在 1–65535 之间")
	}
	if c.PublicHost != "" && !ValidHost(c.PublicHost) {
		return errors.New("对外地址须为域名或 IPv4，不包含协议、路径或端口")
	}
	return nil
}
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return security.WriteAtomic(path, append(b, '\n'), 0644)
}
func (c Config) Address() string { return net.JoinHostPort(c.ListenHost, strconv.Itoa(c.Port)) }
func (c Config) LocalHealthURL() string {
	host := c.ListenHost
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	return c.Scheme() + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port)) + "/api/v1/health"
}
func (c Config) URL(host string) string {
	url := c.Scheme() + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port))
	if c.Entry != "" {
		url += "/" + c.Entry
	}
	return url
}
func LocalHost() string {
	addresses, _ := net.InterfaceAddrs()
	for _, a := range addresses {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
	}
	return "127.0.0.1"
}
func DetectPublicHost(client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("公网 IP 查询状态 %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 65))
	if err != nil {
		return "", err
	}
	host := strings.TrimSpace(string(b))
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", errors.New("公网 IP 查询返回无效 IPv4")
	}
	return host, nil
}

func (c Config) Scheme() string {
	if c.Protocol == "http" {
		return "http"
	}
	return "https"
}
