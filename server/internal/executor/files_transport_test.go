package executor

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesThroughUnixSocketStreamingAndInterruptedChunk(t *testing.T) {
	e, dir := fileFixture(t)
	socketDir, err := os.MkdirTemp("", "ov-sock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "executor.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(e.handle)}
	defer srv.Close()
	go func() { _ = srv.Serve(listener) }()
	client := NewClient(socket)
	defer client.HTTP.CloseIdleConnections()
	op := model.Operation{Name: "files.upload-create", Params: model.Config{"scope": "server", "path": filepath.Join(dir, "stream.bin"), "size": fileChunkLimit + 3}}
	session, err := client.Execute(context.Background(), op, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	upload := func(offset int64, data []byte, length int64, want int) {
		t.Helper()
		req := httptest.NewRequest("PUT", fmtUpload(session.String("id"), int(offset)), bytes.NewReader(data))
		req.ContentLength = length
		rec := httptest.NewRecorder()
		if err := client.FileTransfer(context.Background(), req, rec); err != nil {
			if length != int64(len(data)) {
				return
			}
			t.Fatal(err)
		}
		if rec.Code != want {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	upload(0, []byte("short"), 20, 400)
	// The incomplete body did not advance the committed session offset.
	status, err := e.fileUploadOperation(model.Config{"id": session.String("id")}, "upload-status")
	if err != nil || status["offset"].(int64) != 0 {
		t.Fatal(status, err)
	}
	payload := bytes.Repeat([]byte("x"), fileChunkLimit)
	upload(0, payload, int64(len(payload)), 200)
	upload(fileChunkLimit, []byte("end"), 3, 200)
	_, err = client.Execute(context.Background(), model.Operation{Name: "files.upload-finish", Params: model.Config{"scope": "server", "id": session.String("id")}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/file-transfer?path="+op.Params.String("path"), nil)
	req.Header.Set("Range", "bytes=8388607-8388610")
	rec := httptest.NewRecorder()
	if err = client.FileTransfer(context.Background(), req, rec); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 206 || rec.Body.String() != "xend" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// Cancellation remains a structured context error, never a successful upload.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = client.Execute(ctx, op, func(string) {}); err == nil {
		t.Fatal("cancelled request executed")
	}
}

// Control responses carry text documents, whose JSON encoding can be larger
// than their UTF-8 bytes. Exercise both request and result limits over a socket.
func TestFilesThroughUnixSocketTextLimit(t *testing.T) {
	e, dir := fileFixture(t)
	socketDir, err := os.MkdirTemp("", "ov-text-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "executor.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(e.handle)}
	defer srv.Close()
	go func() { _ = srv.Serve(listener) }()
	client := NewClient(socket)
	defer client.HTTP.CloseIdleConnections()
	path := filepath.Join(dir, "large.txt")
	original := strings.Repeat("x", 3<<20)
	if err = os.WriteFile(path, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	read := model.Operation{Name: "files.read", Params: model.Config{"scope": "server", "path": path}}
	data, err := client.Execute(context.Background(), read, func(string) {})
	if err != nil || data.String("text") != original {
		t.Fatalf("large result failed: %v", err)
	}
	text := strings.Repeat("\x01", fileTextLimit)
	_, err = client.Execute(context.Background(), model.Operation{Name: "files.write", Params: model.Config{"scope": "server", "path": path, "text": text, "revision": data.String("revision")}}, func(string) {})
	if err != nil {
		t.Fatal("escaped request failed", err)
	}
	data, err = client.Execute(context.Background(), read, func(string) {})
	if err != nil || data.String("text") != text {
		t.Fatalf("escaped result failed: %v", err)
	}
}
