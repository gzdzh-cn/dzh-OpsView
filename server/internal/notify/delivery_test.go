package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"opsview/internal/model"
	"testing"
	"time"
)

func TestWebhookSignatureAndAcknowledgement(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	secret := "fixture-secret"
	c := model.Config{"type": "dingtalk", "url": "https://example.com/hook?access_token=fixture", "secret": secret}
	req, err := webhookRequest(context.Background(), c, "title", "message", now)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1700000000000\n" + secret))
	if req.URL.Query().Get("sign") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) || req.URL.Query().Get("timestamp") != "1700000000000" {
		t.Fatal("invalid signature")
	}
	c["type"] = "feishu"
	req, err = webhookRequest(context.Background(), c, "title", "message", now)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(req.Body)
	v := model.Config{}
	_ = json.Unmarshal(data, &v)
	mac = hmac.New(sha256.New, []byte("1700000000\n"+secret))
	if v.String("sign") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
		t.Fatal("invalid Feishu signature")
	}
	for _, kind := range []string{"dingtalk", "feishu", "wecom"} {
		for _, body := range []string{`{}`, `{"code":7,"errcode":7}`, `not-json`} {
			if webhookResponse(kind, []byte(body)) == nil {
				t.Fatal("false success", kind, body)
			}
		}
	}
	if webhookResponse("dingtalk", []byte(`{"errcode":0}`)) != nil || webhookResponse("feishu", []byte(`{"code":0}`)) != nil || webhookResponse("webhook", nil) != nil {
		t.Fatal("valid acknowledgement rejected")
	}
}
