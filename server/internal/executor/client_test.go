package executor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"opsview/internal/model"
	"strings"
	"testing"
)

type resultTransport func(*http.Request) (*http.Response, error)

func (f resultTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestExecutorTerminalFrames(t *testing.T) {
	terminal, err := json.Marshal(model.Result{Done: true, Data: model.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(terminal), `"data":{}`) {
		t.Fatal("empty success was omitted", string(terminal))
	}
	for _, tc := range []struct {
		name, body string
		fails      bool
	}{
		{"empty-success", string(terminal) + "\n", false},
		{"legacy-success", "{\"log\":\"working\"}\n{\"data\":{\"ok\":true}}\n", false},
		{"legacy-empty-success", "{\"data\":{}}\n", false},
		{"interrupted", "{\"log\":\"working\"}\n", true},
		{"ambiguous-old-omission", "{}\n", true},
		{"failure", "{\"done\":true,\"error\":\"real failure\"}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{HTTP: &http.Client{Transport: resultTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}}
			_, err := client.Execute(context.Background(), model.Operation{Name: "site.delete"}, func(string) {})
			if (err != nil) != tc.fails {
				t.Fatal(err)
			}
		})
	}
}
