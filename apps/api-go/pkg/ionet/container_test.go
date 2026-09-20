package ionet

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

type containerHTTPFunc func(*HTTPRequest) (*HTTPResponse, error)

func (f containerHTTPFunc) Do(req *HTTPRequest) (*HTTPResponse, error) { return f(req) }

func TestContainerQueriesPreserveRequestAndResponse(t *testing.T) {
	const rawLogs = "first\r\n\n  second\n"
	for _, tc := range []struct {
		name string
		path string
		body string
		run  func(*testing.T, *Client)
	}{
		{
			name: "list",
			path: "/deployment/deployment/containers",
			body: `{"data":{"total":1,"workers":[{"container_id":"container","status":"running"}]}}`,
			run: func(t *testing.T, client *Client) {
				got, err := client.ListContainers("deployment")
				if err != nil {
					t.Fatal(err)
				}
				if got.Total != 1 || len(got.Workers) != 1 || got.Workers[0].ContainerID != "container" {
					t.Fatalf("unexpected containers: %+v", got)
				}
			},
		},
		{
			name: "details",
			path: "/deployment/deployment/container/container",
			body: `{"container_id":"container","status":"running"}`,
			run: func(t *testing.T, client *Client) {
				got, err := client.GetContainerDetails("deployment", "container")
				if err != nil {
					t.Fatal(err)
				}
				if got.ContainerID != "container" || got.Status != "running" {
					t.Fatalf("unexpected container: %+v", got)
				}
			},
		},
		{
			name: "raw logs",
			path: "/deployment/deployment/log/container",
			body: rawLogs,
			run: func(t *testing.T, client *Client) {
				got, err := client.GetContainerLogsRaw("deployment", "container", nil)
				if err != nil || got != rawLogs {
					t.Fatalf("raw logs changed: %q, error: %v", got, err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClientWithConfig("fixture", "https://example.invalid", containerHTTPFunc(func(req *HTTPRequest) (*HTTPResponse, error) {
				calls++
				if req.Method != http.MethodGet || req.URL != "https://example.invalid"+tc.path || len(req.Body) != 0 {
					t.Fatalf("unexpected request: method=%s url=%s", req.Method, req.URL)
				}
				if req.Headers["X-API-KEY"] != "fixture" {
					t.Fatal("missing authentication header")
				}
				return &HTTPResponse{StatusCode: http.StatusOK, Body: []byte(tc.body)}, nil
			}))
			tc.run(t, client)
			if calls != 1 {
				t.Fatalf("got %d calls, want exactly one", calls)
			}
		})
	}
}

func TestContainerLogsPreserveOptions(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	opts := GetLogsOptions{Level: "info", Stream: "stdout", Limit: 20, Cursor: "a+/= b", Follow: true, StartTime: &start, EndTime: &end}
	before := opts
	client := NewClientWithConfig("fixture", "https://example.invalid", containerHTTPFunc(func(req *HTTPRequest) (*HTTPResponse, error) {
		u, err := url.Parse(req.URL)
		if err != nil {
			t.Fatal(err)
		}
		want := url.Values{
			"level": {"info"}, "stream": {"stdout"}, "limit": {"20"},
			"cursor": {"a+/= b"}, "follow": {"true"},
			"start_time": {start.Format(time.RFC3339)}, "end_time": {end.Format(time.RFC3339)},
		}
		if !reflect.DeepEqual(u.Query(), want) {
			t.Fatalf("query = %v, want %v", u.Query(), want)
		}
		return &HTTPResponse{StatusCode: http.StatusOK}, nil
	}))
	if _, err := client.GetContainerLogsRaw("deployment", "container", &opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts, before) {
		t.Fatalf("caller options mutated: %+v", opts)
	}
}

func TestContainerQueriesRejectMissingIDsBeforeRequest(t *testing.T) {
	client := NewClientWithConfig("fixture", "https://example.invalid", containerHTTPFunc(func(*HTTPRequest) (*HTTPResponse, error) {
		t.Fatal("invalid IDs must not reach the transport")
		return nil, nil
	}))
	if _, err := client.ListContainers(""); err == nil {
		t.Error("list accepted an empty deployment ID")
	}
	for _, ids := range [][2]string{{"", "container"}, {"deployment", ""}} {
		if _, err := client.GetContainerDetails(ids[0], ids[1]); err == nil {
			t.Error("details accepted an empty ID")
		}
		if _, err := client.GetContainerLogsRaw(ids[0], ids[1], nil); err == nil {
			t.Error("logs accepted an empty ID")
		}
	}
}

func TestContainerQueriesPreserveTransportErrors(t *testing.T) {
	failure := errors.New("fixture transport failure")
	client := NewClientWithConfig("fixture", "https://example.invalid", containerHTTPFunc(func(*HTTPRequest) (*HTTPResponse, error) {
		return nil, failure
	}))
	_, listErr := client.ListContainers("deployment")
	_, detailErr := client.GetContainerDetails("deployment", "container")
	_, logErr := client.GetContainerLogsRaw("deployment", "container", nil)
	for _, err := range []error{listErr, detailErr, logErr} {
		if !errors.Is(err, failure) {
			t.Errorf("lost transport error: %v", err)
		}
	}
}
