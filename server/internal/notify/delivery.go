package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"opsview/internal/model"
	"opsview/internal/outbound"
	"strconv"
	"strings"
	"time"
)

var Types = map[string]string{"smtp": "SMTP 邮件", "dingtalk": "钉钉", "feishu": "飞书", "wecom": "企业微信", "webhook": "通用 Webhook"}

func Validate(c model.Config) error {
	if _, ok := Types[c.String("type")]; !ok {
		return errors.New("通知渠道类型无效")
	}
	if c.String("type") == "smtp" {
		if c.String("host") == "" || c.Int("port") < 1 || c.Int("port") > 65535 || c.String("from") == "" || c.String("to") == "" {
			return errors.New("请填写 SMTP 主机、端口、发件人与收件人")
		}
		for _, k := range []string{"host", "from", "to", "username"} {
			if strings.ContainsAny(c.String(k), "\r\n\x00") {
				return errors.New("邮件字段包含无效字符")
			}
		}
		return nil
	}
	if err := outbound.Validate(c.String("url")); err != nil {
		return err
	}
	if !strings.HasPrefix(c.String("url"), "https://") {
		return errors.New("通知 Webhook 必须使用 HTTPS")
	}
	return nil
}
func Send(ctx context.Context, c model.Config, title, text string) error {
	if err := Validate(c); err != nil {
		return err
	}
	if c.String("type") == "smtp" {
		return sendSMTP(ctx, c, title, text)
	}
	req, err := webhookRequest(ctx, c, title, text, time.Now())
	if err != nil {
		return err
	}
	resp, err := outbound.Client(15*time.Second, c.Bool("allowPrivate")).Do(req)
	if err != nil {
		return errors.New("通知发送失败，请检查地址、网络和渠道配置")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("通知服务返回 HTTP %d", resp.StatusCode)
	}
	if err = webhookResponse(c.String("type"), body); err != nil {
		return err
	}
	return nil
}
func webhookRequest(ctx context.Context, c model.Config, title, text string, now time.Time) (*http.Request, error) {
	rawURL := c.String("url")
	payload := model.Config{"title": title, "message": text}
	switch c.String("type") {
	case "dingtalk":
		payload = model.Config{"msgtype": "text", "text": model.Config{"content": title + "\n" + text}}
		if secret := c.String("secret"); secret != "" {
			stamp := strconv.FormatInt(now.UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(stamp + "\n" + secret))
			u, _ := url.Parse(rawURL)
			q := u.Query()
			q.Set("timestamp", stamp)
			q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			u.RawQuery = q.Encode()
			rawURL = u.String()
		}
	case "feishu":
		payload = model.Config{"msg_type": "text", "content": model.Config{"text": title + "\n" + text}}
		if secret := c.String("secret"); secret != "" {
			stamp := strconv.FormatInt(now.Unix(), 10)
			mac := hmac.New(sha256.New, []byte(stamp+"\n"+secret))
			payload["timestamp"] = stamp
			payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
	case "wecom":
		payload = model.Config{"msgtype": "text", "text": model.Config{"content": title + "\n" + text}}
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", rawURL, strings.NewReader(string(b)))
	if err != nil {
		return nil, errors.New("通知地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.String("token") != "" {
		req.Header.Set("Authorization", "Bearer "+c.String("token"))
	}

	return req, nil
}
func webhookResponse(kind string, body []byte) error {
	if kind == "webhook" {
		return nil
	}
	v := model.Config{}
	if json.Unmarshal(body, &v) != nil {
		return errors.New("通知服务返回无效 JSON")
	}
	keys := []string{"errcode"}
	if kind == "feishu" {
		keys = []string{"code", "StatusCode"}
	}
	for _, key := range keys {
		if n, ok := v[key].(float64); ok {
			if n == 0 {
				return nil
			}
			return errors.New("通知渠道拒绝消息，请检查机器人授权与签名")
		}
	}
	return errors.New("通知服务未返回明确的成功状态")
}
func sendSMTP(ctx context.Context, c model.Config, title, text string) error {
	host := c.String("host")
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return errors.New("SMTP DNS 解析失败")
	}
	for _, ip := range ips {
		if !outbound.Allowed(ip.IP, c.Bool("allowPrivate")) {
			return errors.New("SMTP 地址被访问策略拒绝")
		}
	}
	address := net.JoinHostPort(ips[0].IP.String(), strconv.Itoa(c.Int("port")))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return errors.New("SMTP 连接失败")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	var client *smtp.Client
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if c.Int("port") == 465 {
		secure := tls.Client(conn, config)
		if err = secure.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS 校验失败")
		}
		client, err = smtp.NewClient(secure, host)
	} else {
		client, err = smtp.NewClient(conn, host)
		if err == nil {
			err = client.StartTLS(config)
		}
	}
	if err != nil {
		return errors.New("SMTP 要求有效 TLS/STARTTLS")
	}
	defer client.Close()
	if c.String("username") != "" {
		if err = client.Auth(smtp.PlainAuth("", c.String("username"), c.String("password"), host)); err != nil {
			return errors.New("SMTP 认证失败")
		}
	}
	if err = client.Mail(c.String("from")); err != nil {
		return errors.New("发件人被 SMTP 拒绝")
	}
	recipients := strings.FieldsFunc(c.String("to"), func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	for _, to := range recipients {
		if err = client.Rcpt(to); err != nil {
			return errors.New("收件人被 SMTP 拒绝")
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	subject := base64.StdEncoding.EncodeToString([]byte(title))
	_, err = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?%s?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", c.String("from"), strings.Join(recipients, ","), subject, text)
	if err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
