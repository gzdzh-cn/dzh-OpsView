package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"golang.org/x/crypto/bcrypt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"opsview/internal/model"
	"opsview/internal/scheduling"
	"opsview/internal/security"
	"strings"
	"testing"
	"time"
)

func TestRootApprovalBindsContentAndRequiresReverificationOnEnable(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	write := func(method, path string, body any, reauthenticate bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Host = "panel.test"
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		if reauthenticate {
			req.Header.Set("X-OpsView-Password", "test-long-password-123")
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	cfg := model.Config{"operation": "shell", "cron": "* * * * *", "script": "exit 0", "user": "root", "workingDirectory": "/tmp", "timeoutSeconds": 60, "enabled": false}
	w := write("POST", "/api/v1/resources", model.Config{"kind": "schedule", "name": "authorized-root", "config": cfg}, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created model.Resource
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	stored, err := c.App.Store.Resource(created.ID)
	if err != nil || stored.Config.String("rootApproval") != scheduling.Digest(stored.Config) {
		t.Fatal("approval not bound to stored revision", err)
	}
	if strings.Contains(w.Body.String(), "rootApproval") || strings.Contains(w.Body.String(), "exit 0") {
		t.Fatal("sensitive script content returned in generic response")
	}
	stored.Config["enabled"] = true
	body := model.Config{"name": stored.Name, "revision": stored.Revision, "config": stored.Config}
	path := "/api/v1/resources/" + stored.ID
	if w = write("PUT", path, body, false); w.Code != 403 {
		t.Fatal("enabling root plan reused old authorization", w.Code, w.Body.String())
	}
	if w = write("PUT", path, body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	stored, _ = c.App.Store.Resource(created.ID)
	stored.Config["script"] = "exit 7"
	body = model.Config{"name": stored.Name, "revision": stored.Revision, "config": stored.Config}
	if w = write("PUT", path, body, false); w.Code != 403 {
		t.Fatal("changed content reused old authorization", w.Code, w.Body.String())
	}
	current, _ := c.App.Store.Resource(created.ID)
	if current.Config.String("script") != "exit 0" || current.Revision != stored.Revision {
		t.Fatal("rejected change altered saved script")
	}
}

func TestScheduleMutationsRequirePOSTAndRootApproval(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	cfg := model.Config{"operation": "shell", "cron": "* * * * *", "script": "exit 0", "user": "root", "workingDirectory": "/tmp", "timeoutSeconds": 60}
	w := request(h, "POST", "/api/v1/resources", model.Config{"kind": "schedule", "name": "root-unapproved", "config": cfg}, cookie, csrf, "")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg["user"] = "opsview-task"
	r := model.Resource{Kind: "schedule", Name: "script", Config: cfg}
	if err := c.App.Store.Create(&r); err != nil {
		t.Fatal(err)
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "schedule.execute", Resource: r, Params: model.Config{"scheduleId": r.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"stop", "clear-history"} {
		w = request(h, "GET", "/api/v1/schedules/"+r.ID+"/"+view, nil, cookie, "", "")
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal(view, w.Code, w.Body.String())
		}
	}
	found, _ := c.App.Store.Task(task.ID)
	if found.State != "queued" {
		t.Fatal("GET changed state")
	}
	w = request(h, "POST", "/api/v1/schedules/"+r.ID+"/stop", nil, cookie, "", "")
	if w.Code != 403 {
		t.Fatal("csrf bypass", w.Code)
	}
	w = request(h, "POST", "/api/v1/schedules/"+r.ID+"/stop", model.Config{}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestTemporaryGrantOneUseAndReadonly(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	w := request(h, "POST", "/api/v1/auth/tokens/temporary", model.Config{"name": "review", "password": "test-long-password-123", "scopes": []string{"resources:read"}}, cookie, csrf, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created model.Config
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	w = request(h, "POST", "/api/v1/auth/temporary", model.Config{"token": created.String("token")}, nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	grant := w.Result().Cookies()[0]
	w = request(h, "POST", "/api/v1/auth/temporary", model.Config{"token": created.String("token")}, nil, "", "")
	if w.Code != 403 {
		t.Fatal("reused", w.Code)
	}
	w = request(h, "GET", "/api/v1/resources", nil, grant, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("sensitive response cached")
	}
	w = request(h, "POST", "/api/v1/resources", model.Config{"kind": "schedule", "name": "bad"}, grant, "", "")
	if w.Code != 403 {
		t.Fatal("write accepted", w.Code)
	}
	w = request(h, "GET", "/api/v1/settings", nil, grant, "", "")
	if w.Code != 403 {
		t.Fatal("settings leaked", w.Code)
	}
	_, _, _, _, err := c.App.Store.Token(security.Hash(created.String("token")))
	if err == nil {
		t.Fatal("original token still usable")
	}
}
func TestClosingEntranceKeepsTaskQueueAndLocalHealth(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	var p model.Config
	rev, _ := c.App.Store.SettingRevision("panel", &p)
	w := request(h, "PATCH", "/api/v1/settings/groups/panel", model.Config{"patch": model.Config{"webDisabled": true}, "revision": rev, "password": "test-long-password-123"}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/resources", nil, cookie, "", "")
	if w.Code != 503 {
		t.Fatal("entrance still open", w.Code)
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "schedule.execute", Resource: model.Resource{ID: "background"}, Params: model.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.App.Store.Claim(); err != nil {
		t.Fatal(err)
	}
	if err = c.App.Store.Finish(task.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	// Health remains available to the loopback maintenance client.
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestAPIHTTPSScopesAndPasskeyMethods(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	w := request(h, "POST", "/api/v1/auth/tokens/api", model.Config{"name": "integration", "days": 1, "scopes": []string{"resources:read"}, "password": "test-long-password-123"}, cookie, csrf, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var token model.Config
	_ = json.Unmarshal(w.Body.Bytes(), &token)
	if err := c.App.Store.Set("panel", model.Config{"apiEnabled": true}); err != nil {
		t.Fatal(err)
	}
	for _, secure := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/v1/resources", nil)
		req.Host = "panel.test"
		req.Header.Set("Authorization", "Bearer "+token.String("token"))
		if secure {
			req.TLS = &tls.ConnectionState{}
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		expected := 403
		if secure {
			expected = 200
		}
		if out.Code != expected {
			t.Fatal(secure, out.Code, out.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/settings", nil)
	req.Host = "panel.test"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Authorization", "Bearer "+token.String("token"))
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 403 {
		t.Fatal("scope bypass", out.Code)
	}
	if w = request(h, "GET", "/api/v1/auth/passkeys/delete", nil, cookie, "", ""); w.Code != 405 {
		t.Fatal("passkey GET mutation allowed", w.Code)
	}
	if w = request(h, "GET", "/api/v1/auth/passkeys/list", nil, cookie, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "reason") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBasicAuthAndClientCertificateAreAdditionalFactors(t *testing.T) {
	c, h := testController(t)
	cookie, _ := setupAndLogin(t, h)
	hash, _ := bcrypt.GenerateFromPassword([]byte("basic-fixture-password"), bcrypt.MinCost)
	if err := c.App.Store.Set("panel", model.Config{"basicEnabled": true, "basicUsername": "operator", "basicPasswordHash": string(hash)}); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/api/v1/resources", nil, cookie, "", ""); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("BasicAuth not required", w.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/resources", nil)
	req.Host = "panel.test"
	req.SetBasicAuth("operator", "basic-fixture-password")
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/v1/resources", nil)
	req.Host = "panel.test"
	req.SetBasicAuth("operator", "basic-fixture-password")
	out = httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 401 {
		t.Fatal("basic replaced administrator authentication", out.Code)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	parsedCA, _ := x509.ParseCertificate(caDER)
	client := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Fixture client"}, KeyUsage: x509.KeyUsageDigitalSignature, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	clientDER, _ := x509.CreateCertificate(rand.Reader, client, ca, &key.PublicKey, key)
	parsedClient, _ := x509.ParseCertificate(clientDER)
	if err := c.App.Store.Set("panel", model.Config{"clientCertificateEnabled": true, "clientCA": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))}); err != nil {
		t.Fatal(err)
	}
	// A certificate alone never authenticates the administrator.
	for _, withAdmin := range []bool{false, true} {
		req = httptest.NewRequest("GET", "/api/v1/resources", nil)
		req.Host = "panel.test"
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{parsedClient, parsedCA}}
		if withAdmin {
			secureCookie := *cookie
			secureCookie.Name = "opsview_session"
			req.AddCookie(&secureCookie)
		}
		out = httptest.NewRecorder()
		h.ServeHTTP(out, req)
		expected := 401
		if withAdmin {
			expected = 200
		}
		if out.Code != expected {
			t.Fatal(withAdmin, out.Code, out.Body.String())
		}
	}
}
