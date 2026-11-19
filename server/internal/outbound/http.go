// Package outbound pins validated DNS answers for each connection and each redirect.
package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func Allowed(ip net.IP, private bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.String() == "100.100.100.200" || ip.String() == "168.63.129.16" {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || isSharedAddress(ip) {
		return private
	}
	return ip.IsGlobalUnicast()
}
func Validate(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("只允许不带内嵌凭据的 HTTP/HTTPS URL")
	}
	return nil
}
func Client(timeout time.Duration, private bool) *http.Client {
	tr := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: timeout, DisableKeepAlives: true}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("目标 DNS 解析失败")
		}
		if len(ips) == 0 {
			return nil, errors.New("目标没有有效地址")
		}
		for _, ip := range ips {
			if !Allowed(ip.IP, private) {
				return nil, errors.New("目标地址被拒绝；内网任务须显式允许，元数据和链路本地地址始终禁止")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向超过五次")
		}
		if err := Validate(req.URL.String()); err != nil {
			return err
		}
		if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			return errors.New("拒绝跨主机重定向，以免泄露请求凭据")
		}
		return nil
	}}
}

func isSharedAddress(ip net.IP) bool {
	_, block, _ := net.ParseCIDR("100.64.0.0/10")
	return block.Contains(ip)
}
