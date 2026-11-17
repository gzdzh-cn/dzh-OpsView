package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"opsview/internal/model"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Negotiation is shared by all Engine API operations, including older managed instances.
type dockerGateway struct {
	http     *http.Client
	mu       sync.Mutex
	version  string
	expires  time.Time
	metadata model.Config
}

var dockerAPI = &dockerGateway{http: dockerHTTP()}

func apiNumber(v string) (int, error) {
	p := strings.Split(v, ".")
	if len(p) != 2 {
		return 0, errors.New("Docker API 版本格式无效")
	}
	major, e := strconv.Atoi(p[0])
	if e != nil || major != 1 {
		return 0, errors.New("Docker API 主版本不受支持")
	}
	minor, e := strconv.Atoi(p[1])
	if e != nil || minor < 0 {
		return 0, errors.New("Docker API 版本无效")
	}
	return minor, nil
}
func (g *dockerGateway) negotiate(ctx context.Context) (string, model.Config, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Now().Before(g.expires) {
		return g.version, cloneConfig(g.metadata), nil
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(probe, "GET", "http://docker/version", nil)
	res, err := g.http.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", nil, fmt.Errorf("Docker 探测返回 HTTP %d", res.StatusCode)
	}
	var v struct {
		Version       string
		APIVersion    string `json:"ApiVersion"`
		MinAPIVersion string
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&v); err != nil {
		return "", nil, err
	}
	max, err := apiNumber(v.APIVersion)
	if err != nil {
		return "", nil, err
	}
	min := 0
	if v.MinAPIVersion != "" {
		min, err = apiNumber(v.MinAPIVersion)
		if err != nil {
			return "", nil, err
		}
	}
	metadata := model.Config{"engineVersion": v.Version, "maxAPIVersion": v.APIVersion, "minAPIVersion": v.MinAPIVersion}
	chosen := 47
	if max < chosen {
		chosen = max
	}
	if chosen < 41 || min > chosen {
		return "", metadata, errors.New("Docker API 与面板支持的 1.41–1.47 无交集，请手动升级兼容引擎")
	}
	g.version = fmt.Sprintf("1.%d", chosen)
	metadata["apiVersion"] = g.version
	g.metadata = metadata
	g.expires = time.Now().Add(30 * time.Second)
	return g.version, cloneConfig(metadata), nil
}
func (g *dockerGateway) invalidate() { g.mu.Lock(); g.expires = time.Time{}; g.mu.Unlock() }
func (g *dockerGateway) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	attempts := 1
	if method == "GET" {
		attempts = 2
	}
	for attempt := 0; attempt < attempts; attempt++ {
		version, _, er := g.negotiate(ctx)
		if er != nil {
			return nil, fmt.Errorf("Docker 引擎连接或 API 协商失败：%w", er)
		}
		req, er := http.NewRequestWithContext(ctx, method, "http://docker/v"+version+path, bytes.NewReader(payload))
		if er != nil {
			return nil, er
		}
		req.Header.Set("Content-Type", "application/json")
		res, er := g.http.Do(req)
		if er != nil {
			g.invalidate()
			if attempt+1 < attempts {
				continue
			}
			return nil, fmt.Errorf("Docker 引擎连接失败：%w", er)
		}
		data, er := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		res.Body.Close()
		if er != nil {
			return nil, er
		}
		if res.StatusCode >= 300 {
			var message struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(data, &message)
			if message.Message == "" {
				message.Message = strings.TrimSpace(string(data))
			}
			if res.StatusCode == 400 && strings.Contains(strings.ToLower(message.Message), "version") {
				g.invalidate()
				if attempt+1 < attempts {
					continue
				}
			}
			return nil, &dockerAPIError{code: res.StatusCode, message: message.Message}
		}
		return data, nil
	}
	return nil, errors.New("Docker 请求失败")
}
func (e *Engine) dockerStatus(ctx context.Context) model.Config {
	_, metadata, err := dockerAPI.negotiate(ctx)
	if metadata == nil {
		metadata = model.Config{}
	}
	status := "ready"
	reason := "Docker 引擎可用"
	if err != nil {
		reason = "无法检测 Docker 引擎：" + err.Error()
		status = "error"
		if metadata.String("engineVersion") != "" {
			status = "incompatible"
			reason = err.Error()
		} else if _, socketErr := os.Stat("/var/run/docker.sock"); os.IsNotExist(socketErr) {
			if _, binaryErr := exec.LookPath("docker"); binaryErr != nil {
				status = "not_installed"
				reason = "尚未安装 Docker 引擎"
			} else {
				status = "stopped"
				reason = "Docker 已安装，服务尚未启动"
			}
		}
	}
	metadata["status"] = status
	metadata["reason"] = reason
	composeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	compose, composeErr := output(composeCtx, "docker", "compose", "version", "--short")
	metadata["composeAvailable"] = composeErr == nil
	if composeErr == nil {
		metadata["composeVersion"] = strings.TrimSpace(compose)
	}
	return metadata
}
