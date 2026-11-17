package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type dockerRoundTrip func(*http.Request) (*http.Response, error)

func (f dockerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func dockerResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func TestDockerNegotiatesHighestIntersectionAndCaches(t *testing.T) {
	for _, tc := range []struct {
		max, min, want string
		bad            bool
	}{{"1.41", "1.12", "1.41", false}, {"1.51", "1.44", "1.47", false}, {"1.40", "1.12", "", true}, {"1.50", "1.48", "", true}, {"garbage", "", "", true}} {
		t.Run(tc.max+tc.min, func(t *testing.T) {
			probes := 0
			g := &dockerGateway{http: &http.Client{Transport: dockerRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/version" {
					t.Fatalf("version probe path %s", r.URL.Path)
				}
				probes++
				return dockerResponse(200, fmt.Sprintf(`{"Version":"20.10.24","ApiVersion":%q,"MinAPIVersion":%q}`, tc.max, tc.min)), nil
			})}}
			got, _, err := g.negotiate(context.Background())
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("got %q %v", got, err)
			}
			if tc.bad {
				return
			}
			_, _, _ = g.negotiate(context.Background())
			if probes != 1 {
				t.Fatal("cache missed")
			}
			g.expires = time.Now().Add(-time.Second)
			_, _, _ = g.negotiate(context.Background())
			if probes != 2 {
				t.Fatal("expired cache reused")
			}
		})
	}
}
func TestDockerOnlyReadRequestsRetry(t *testing.T) {
	for _, method := range []string{"GET", "POST", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			probes, calls := 0, 0
			g := &dockerGateway{http: &http.Client{Transport: dockerRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/version" {
					probes++
					return dockerResponse(200, `{"Version":"20.10.24","ApiVersion":"1.41"}`), nil
				}
				if !strings.HasPrefix(r.URL.Path, "/v1.41/") {
					t.Fatalf("wrong negotiated URL %s", r.URL.Path)
				}
				calls++
				if calls == 1 {
					return dockerResponse(400, `{"message":"client version is too new"}`), nil
				}
				return dockerResponse(200, `[]`), nil
			})}}
			_, err := g.request(context.Background(), method, "/containers/json", nil)
			if method == "GET" {
				if err != nil || calls != 2 || probes != 2 {
					t.Fatal(calls, probes, err)
				}
			} else if err == nil || calls != 1 {
				t.Fatal("mutation replayed", calls, err)
			}
		})
	}
}
