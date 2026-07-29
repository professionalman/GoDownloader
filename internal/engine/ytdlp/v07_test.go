package ytdlp

import (
	"slices"
	"testing"

	"downloader/internal/networkpolicy"
)

func TestNetworkPolicyUsesSeparateArgumentsAndRetrySemantics(t *testing.T) {
	args := appendNetworkArgs(nil, &networkpolicy.RuntimePolicy{
		Policy: networkpolicy.JobNetworkPolicy{
			DownloadLimitBytesPerSecond: 4096,
			Proxy:                       networkpolicy.ProxyPolicy{Mode: networkpolicy.ProxyModeCustom, Protocol: networkpolicy.ProxyProtocolSOCKS5, Host: "proxy.local", Port: 1080},
			UserAgent:                   "agent",
			HTTPHeaders:                 []networkpolicy.HTTPHeader{{Name: "Authorization", Sensitive: true}},
			RetryPolicy:                 networkpolicy.RetryPolicy{MaxAttempts: 3, RetryWaitSeconds: 4},
			TimeoutPolicy:               networkpolicy.TimeoutPolicy{RequestTimeoutSeconds: 5},
		},
		HeaderValues: map[string]string{"authorization": "Bearer secret"},
	})
	for _, expected := range []string{"--proxy", "socks5://proxy.local:1080", "--limit-rate", "4096", "--socket-timeout", "5", "--retries", "2", "--fragment-retries", "2", "--retry-sleep", "4", "--user-agent", "agent", "--add-header", "Authorization:Bearer secret"} {
		if !slices.Contains(args, expected) {
			t.Fatalf("missing %q in %#v", expected, args)
		}
	}
}
