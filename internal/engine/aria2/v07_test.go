package aria2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

func TestStartUsesAllowlistedTypedNetworkOptions(t *testing.T) {
	var options map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "aria2.addUri" {
			t.Fatalf("method=%s", request.Method)
		}
		if err := json.Unmarshal(request.Params[len(request.Params)-1], &options); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":"gid"}`))
	}))
	defer server.Close()
	engine := NewEngine(server.URL, "")
	download := &job.Job{ID: "one", Source: "https://example.com/file", NetworkPolicy: networkpolicy.JobNetworkPolicy{}}
	download.SetRuntimeNetworkPolicy(&networkpolicy.RuntimePolicy{
		Policy: networkpolicy.JobNetworkPolicy{
			DownloadLimitBytesPerSecond: 1024,
			Proxy:                       networkpolicy.ProxyPolicy{Mode: networkpolicy.ProxyModeCustom, Protocol: networkpolicy.ProxyProtocolHTTP, Host: "proxy.local", Port: 8080, Username: "u"},
			UserAgent:                   "GoDownloader-Test",
			HTTPHeaders:                 []networkpolicy.HTTPHeader{{Name: "X-Test", Value: "value"}, {Name: "Authorization", Sensitive: true, HasValue: true}},
			RetryPolicy:                 networkpolicy.RetryPolicy{MaxAttempts: 3, RetryWaitSeconds: 2},
			TimeoutPolicy:               networkpolicy.TimeoutPolicy{ConnectTimeoutSeconds: 4, RequestTimeoutSeconds: 5},
			DirectConnections:           &networkpolicy.DirectConnectionPolicy{Split: 5, MaxConnectionsPerServer: 2, MinSplitSizeBytes: 20 << 20},
		},
		ProxyPassword: "password",
		HeaderValues:  map[string]string{"authorization": "Bearer secret"},
	})
	if _, err := engine.Start(context.Background(), download, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	headers, ok := options["header"].([]any)
	if !ok || len(headers) != 2 {
		t.Fatalf("headers are not an RPC array: %#v", options["header"])
	}
	if options["max-download-limit"] != "1024" || options["max-tries"] != "3" || options["all-proxy-passwd"] != "password" {
		t.Fatalf("unexpected options: %#v", options)
	}
	if _, exists := options["raw-options"]; exists {
		t.Fatal("raw passthrough reached aria2")
	}
}
