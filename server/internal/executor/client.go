package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"opsview/internal/model"
	"time"
)

type Client struct{ HTTP *http.Client }

func NewClient(socket string) *Client {
	return &Client{&http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}, Timeout: 2 * time.Hour}}
}
func (c *Client) Execute(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	b, e := json.Marshal(op)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "http://executor/execute", bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := c.HTTP.Do(req)
	if e != nil {
		return nil, fmt.Errorf("本机执行器不可用：%w", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("执行器拒绝请求：%d", res.StatusCode)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var data model.Config
	finished := false
	for scanner.Scan() {
		var r model.Result
		if e = json.Unmarshal(scanner.Bytes(), &r); e != nil {
			return nil, e
		}
		if r.Error != "" {
			if r.Code == "revision_conflict" {
				return nil, &model.RevisionConflict{Revision: r.Revision}
			}
			return nil, errors.New(r.Error)
		}
		if r.Log != "" {
			log(r.Log)
		}
		if r.Data != nil {
			data = r.Data
			finished = true
		}
	}
	if e = scanner.Err(); e != nil {
		return nil, e
	}
	if !finished {
		return nil, errors.New("执行器连接中断，未收到完成确认")
	}
	return data, nil
}
