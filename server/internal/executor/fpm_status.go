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
	network := "tcp"
	if strings.HasPrefix(address, "/") {
		network = "unix"
	}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	record := func(typ byte, payload []byte) error {
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
	for _, p := range [][2]string{{"REQUEST_METHOD", "GET"}, {"SCRIPT_NAME", "/opsview-fpm-status"}, {"SCRIPT_FILENAME", "/opsview-fpm-status"}, {"REQUEST_URI", "/opsview-fpm-status?json"}, {"QUERY_STRING", "json"}, {"SERVER_PROTOCOL", "HTTP/1.1"}, {"SERVER_NAME", "localhost"}, {"REMOTE_ADDR", "127.0.0.1"}, {"SERVER_ADDR", "127.0.0.1"}, {"SERVER_PORT", "80"}} {
		params = append(params, byte(len(p[0])), byte(len(p[1])))
		params = append(params, p[0]...)
		params = append(params, p[1]...)
	}
	if err = record(4, params); err != nil {
		return nil, err
	}
	if err = record(4, nil); err != nil {
		return nil, err
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
	var result model.Config
	if err = json.Unmarshal([]byte(body), &result); err != nil {
		return nil, errors.New("FPM 状态暂不可用")
	}
	return result, nil
}
func phpStatusAddress(r model.Resource) string {
	return "127.0.0.1:" + strconv.Itoa(r.Config.Int("port"))
}
