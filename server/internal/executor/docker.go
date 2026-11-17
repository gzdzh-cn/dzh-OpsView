package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"opsview/internal/logic"
	"opsview/internal/model"
	"strings"
	"time"
)

type dockerAPIError struct {
	code    int
	message string
}

func (e *dockerAPIError) Error() string { return fmt.Sprintf("Docker %d：%s", e.code, e.message) }

func dockerHTTP() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}, Timeout: 30 * time.Minute}
}
func dockerRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	return dockerAPI.request(ctx, method, path, body)
}
func (e *Engine) docker(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	switch op.Name {
	case "docker.status":
		return e.dockerStatus(ctx), nil
	case "docker.manage":
		return e.dockerManage(ctx, op, log)
	case "docker.list":
		collection := op.Params.String("collection")
		path := ""
		switch collection {
		case "containers":
			path = "/containers/json?all=true"
		case "images":
			path = "/images/json"
		case "networks":
			path = "/networks"
		case "volumes":
			path = "/volumes"
		case "info":
			path = "/info"
		default:
			return nil, errors.New("Docker 资源类型无效")
		}
		b, err := dockerRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		var v any
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return model.Config{"items": v}, nil
	case "docker.action":
		id := op.Params.String("id")
		if !logic.Name.MatchString(id) && !isDockerID(id) {
			return nil, errors.New("容器标识无效")
		}
		action := op.Params.String("action")
		switch action {
		case "start", "stop", "restart":
			_, err := dockerRequest(ctx, "POST", "/containers/"+url.PathEscape(id)+"/"+action, nil)
			return model.Config{}, err
		case "remove":
			for _, resource := range e.states() {
				if (resource.Kind == "instance" || resource.Kind == "compose" || resource.Kind == "plugin") && (id == "opsview-"+resource.ID) {
					return nil, errors.New("请通过实例或 Compose 页面删除受管理的容器")
				}
			}
			inspected, err := dockerRequest(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil)
			if err != nil {
				return nil, err
			}
			var metadata struct {
				Config struct{ Labels map[string]string }
			}
			if err = json.Unmarshal(inspected, &metadata); err != nil {
				return nil, err
			}
			if metadata.Config.Labels["opsview.managed"] == "true" || strings.HasPrefix(metadata.Config.Labels["com.docker.compose.project"], "opsview-") {
				return nil, errors.New("请通过实例或 Compose 页面删除受管理的容器")
			}
			if !op.Params.Bool("confirmContainerDeletion") {
				return nil, errors.New("删除容器需要明确确认，数据卷默认保留")
			}
			_, err = dockerRequest(ctx, "DELETE", "/containers/"+url.PathEscape(id), nil)
			return model.Config{}, err

		case "stats":
			b, err := dockerRequest(ctx, "GET", "/containers/"+url.PathEscape(id)+"/stats?stream=false&one-shot=true", nil)
			if err != nil {
				return nil, err
			}
			var stats model.Config
			err = json.Unmarshal(b, &stats)
			return stats, err
		case "logs":
			b, err := dockerRequest(ctx, "GET", "/containers/"+url.PathEscape(id)+"/logs?stdout=true&stderr=true&tail=200", nil)
			if err != nil {
				return nil, err
			}
			return model.Config{"content": demux(b)}, nil
		default:
			return nil, errors.New("容器操作无效")
		}
	}
	return nil, errors.New("Docker 操作无效")
}
func (e *Engine) dockerManage(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	c := op.Params.String("collection")
	action := op.Params.String("action")
	name := op.Params.String("name")
	switch {
	case c == "images" && action == "pull":
		return model.Config{}, pullImage(ctx, name, log)
	case c == "images" && action == "delete":
		if !strings.HasPrefix(name, "sha256:") || !isDockerID(strings.TrimPrefix(name, "sha256:")) {
			return nil, errors.New("镜像 ID 无效")
		}
		_, err := dockerRequest(ctx, "DELETE", "/images/"+url.PathEscape(name)+"?force=false", nil)
		return model.Config{}, err
	case (c == "networks" || c == "volumes") && action == "create":
		if !logic.Name.MatchString(name) {
			return nil, errors.New("资源名称无效")
		}
		name = "opsview-" + name
		body := map[string]any{"Name": name, "Driver": "bridge", "Labels": map[string]string{"opsview.managed": "true"}}
		path := "/networks/create"
		if c == "volumes" {
			body["Driver"] = "local"
			path = "/volumes/create"
		}
		b, err := dockerRequest(ctx, "POST", path, body)
		if err != nil {
			return nil, err
		}
		var result model.Config
		_ = json.Unmarshal(b, &result)
		return model.Config{"created": result}, nil
	case (c == "networks" || c == "volumes") && action == "delete":
		if !logic.Name.MatchString(name) && !isDockerID(name) {
			return nil, errors.New("资源名称无效")
		}
		if op.Params.String("confirmation") != name {
			return nil, errors.New("请准确输入资源名称以确认删除")
		}
		path := "/" + c + "/" + url.PathEscape(name)
		b, err := dockerRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		var resource struct{ Labels map[string]string }
		if err = json.Unmarshal(b, &resource); err != nil {
			return nil, err
		}
		if resource.Labels["opsview.managed"] != "true" && !strings.HasPrefix(resource.Labels["com.docker.compose.project"], "opsview-") {
			return nil, errors.New("已有外部资源只提供状态检查，不允许面板删除")
		}
		_, err = dockerRequest(ctx, "DELETE", path+"?force=false", nil)
		return model.Config{}, err
	}
	return nil, errors.New("Docker 管理操作无效")
}
func isDockerID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func demux(b []byte) string {
	var out bytes.Buffer
	for len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if n > len(b)-8 {
			break
		}
		out.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	if out.Len() == 0 {
		return string(b)
	}
	return out.String()
}
func pullImage(ctx context.Context, image string, log func(string)) error {
	if !strings.Contains(image, "@sha256:") {
		return errors.New("镜像必须固定 SHA-256 摘要")
	}
	b, err := dockerRequest(ctx, "POST", "/images/create?fromImage="+url.QueryEscape(image), nil)
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var v map[string]any
		if json.Unmarshal(line, &v) == nil {
			if msg, ok := v["error"].(string); ok {
				return errors.New(msg)
			}
			if status, ok := v["status"].(string); ok {
				log(status)
			}
		}
	}
	return nil
}
