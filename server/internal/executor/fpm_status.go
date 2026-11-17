package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"opsview/internal/model"
	"strconv"
	"strings"
	"time"
)

// The status request stays on loopback/Unix sockets and never exposes a diagnostic route on a site.
func fpmStatus(ctx context.Context, address string) (model.Config, error) {
	body, err := fpmRequest(ctx, address, map[string]string{"REQUEST_METHOD": "GET", "SCRIPT_NAME": "/opsview-fpm-status", "SCRIPT_FILENAME": "/opsview-fpm-status", "REQUEST_URI": "/opsview-fpm-status?json", "QUERY_STRING": "json"}, nil)
	if err != nil {
		return nil, err
	}
	var result model.Config
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, errors.New("FPM 状态暂不可用")
	}
	return result, nil
}

// Only executor-generated requests use this private protocol client. It accepts
// no browser-supplied FastCGI parameters or public diagnostic script paths.
func fpmRequest(ctx context.Context, address string, request map[string]string, input []byte) ([]byte, error) {
	network := "tcp"
	if strings.HasPrefix(address, "/") {
		network = "unix"
	}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	record := func(typ byte, payload []byte) error {
		if len(payload) > 65535 {
			return errors.New("FPM 请求过大")
		}
		header := []byte{1, typ, 0, 1, 0, 0, 0, 0}
		binary.BigEndian.PutUint16(header[4:6], uint16(len(payload)))
		if _, er := conn.Write(header); er != nil {
			return er
		}
		_, er := conn.Write(payload)
		return er
	}
	if err = record(1, []byte{0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return nil, err
	}
	params := []byte{}
	for k, v := range map[string]string{"SERVER_PROTOCOL": "HTTP/1.1", "SERVER_NAME": "localhost", "REMOTE_ADDR": "127.0.0.1", "SERVER_ADDR": "127.0.0.1", "SERVER_PORT": "80"} {
		request[k] = v
	}
	for key, value := range request {
		params = appendFCGILength(params, len(key))
		params = appendFCGILength(params, len(value))
		params = append(params, key...)
		params = append(params, value...)
	}
	if err = record(4, params); err != nil {
		return nil, err
	}
	if err = record(4, nil); err != nil {
		return nil, err
	}
	if len(input) > 0 {
		if err = record(5, input); err != nil {
			return nil, err
		}
	}
	if err = record(5, nil); err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	for stdout.Len() < 1<<20 {
		header := make([]byte, 8)
		if _, err = io.ReadFull(conn, header); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint16(header[4:6]))
		payload := make([]byte, n+int(header[6]))
		if _, err = io.ReadFull(conn, payload); err != nil {
			return nil, err
		}
		if header[1] == 6 {
			if stdout.Len()+n > 1<<20 {
				return nil, errors.New("FPM 响应过大")
			}
			stdout.Write(payload[:n])
		}
		if header[1] == 3 {
			break
		}
	}
	_, body, ok := strings.Cut(stdout.String(), "\r\n\r\n")
	if !ok {
		return nil, errors.New("FPM 状态响应格式无效")
	}
	return []byte(body), nil
}

func appendFCGILength(dst []byte, n int) []byte {
	if n < 128 {
		return append(dst, byte(n))
	}
	return binary.BigEndian.AppendUint32(dst, uint32(n)|0x80000000)
}
func phpStatusAddress(r model.Resource) string {
	return "127.0.0.1:" + strconv.Itoa(r.Config.Int("port"))
}
