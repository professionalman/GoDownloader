package networkpolicy

import "testing"

func TestValidationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"bandwidth zero", ValidateBandwidth(0), false},
		{"bandwidth max", ValidateBandwidth(MaxSafeInteger), false},
		{"bandwidth negative", ValidateBandwidth(-1), true},
		{"bandwidth too large", ValidateBandwidth(MaxSafeInteger + 1), true},
		{"retry max", ValidateRetry(RetryPolicy{MaxAttempts: 100, RetryWaitSeconds: 3600}), false},
		{"retry too large", ValidateRetry(RetryPolicy{MaxAttempts: 101}), true},
		{"timeout engine default", ValidateTimeout(TimeoutPolicy{}), false},
		{"timeout too large", ValidateTimeout(TimeoutPolicy{RequestTimeoutSeconds: 86401}), true},
		{"direct minimum", ValidateDirect(&DirectConnectionPolicy{Split: 1, MaxConnectionsPerServer: 1, MinSplitSizeBytes: 1 << 20}), false},
		{"direct maximum", ValidateDirect(&DirectConnectionPolicy{Split: 16, MaxConnectionsPerServer: 16, MinSplitSizeBytes: 1 << 30}), false},
		{"direct split too large", ValidateDirect(&DirectConnectionPolicy{Split: 17, MaxConnectionsPerServer: 1, MinSplitSizeBytes: 1 << 20}), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.err != nil; got != test.want {
				t.Fatalf("error=%v, wantError=%v", test.err, test.want)
			}
		})
	}
}

func TestHeadersAreNormalizedMaskedAndUnique(t *testing.T) {
	headers := []HTTPHeader{{Name: "Authorization", Value: "Bearer secret"}}
	if err := ValidateHeaders(headers); err != nil {
		t.Fatal(err)
	}
	if !headers[0].Sensitive || !headers[0].HasValue {
		t.Fatalf("expected sensitive marker: %+v", headers[0])
	}
	if err := ValidateHeaders([]HTTPHeader{{Name: "X-Test"}, {Name: "x-test"}}); err == nil {
		t.Fatal("expected case-insensitive duplicate rejection")
	}
	if err := ValidateHeaders([]HTTPHeader{{Name: "Host", Value: "example.com"}}); err == nil {
		t.Fatal("expected transport-owned header rejection")
	}
	if err := ValidateHeaders([]HTTPHeader{{Name: "X-Test", Value: "bad\r\nvalue"}}); err == nil {
		t.Fatal("expected header injection rejection")
	}
}

func TestSeedingModesAndTrackerSchemes(t *testing.T) {
	ratio := 2.5
	duration := int64(3600)
	valid := []SeedingPolicy{
		{Mode: SeedingModeNone}, {Mode: SeedingModeUnlimited},
		{Mode: SeedingModeRatio, RatioLimit: &ratio},
		{Mode: SeedingModeDuration, TimeLimitSeconds: &duration},
		{Mode: SeedingModeRatioOrDuration, RatioLimit: &ratio, TimeLimitSeconds: &duration},
	}
	for _, policy := range valid {
		if err := ValidateSeeding(policy); err != nil {
			t.Fatalf("%s rejected: %v", policy.Mode, err)
		}
	}
	if err := ValidateSeeding(SeedingPolicy{Mode: SeedingModeRatio}); err == nil {
		t.Fatal("ratio without threshold should fail")
	}
	if err := ValidateSeeding(SeedingPolicy{Mode: ""}); err == nil || err.Error() != "invalid seeding mode" {
		t.Fatalf("empty seeding mode must be rejected, got: %v", err)
	}
	if err := ValidateSeeding(SeedingPolicy{Mode: "invalid_unknown_mode"}); err == nil || err.Error() != "invalid seeding mode" {
		t.Fatalf("unknown seeding mode must be rejected, got: %v", err)
	}
	values, err := ValidateTrackerURLs([]string{
		"https://tracker.example/announce", "udp://tracker.example:80/announce",
		"ws://tracker.example/announce", "wss://tracker.example/announce",
		"https://tracker.example/announce",
	}, 10)
	if err != nil || len(values) != 4 {
		t.Fatalf("unexpected tracker normalization: %#v, %v", values, err)
	}
	if _, err := ValidateTrackerURLs([]string{"https://user:pass@tracker.example/announce"}, 10); err == nil {
		t.Fatal("userinfo must be rejected")
	}
}

func TestCapabilityIntersection(t *testing.T) {
	a := ProjectCapabilities(EngineCapabilities{PerJobDownloadLimit: true, Proxy: true, ProxyProtocols: []ProxyProtocol{ProxyProtocolHTTP, ProxyProtocolSOCKS5}}, true)
	b := ProjectCapabilities(EngineCapabilities{PerJobDownloadLimit: true, Proxy: true, ProxyProtocols: []ProxyProtocol{ProxyProtocolHTTP}}, true)
	result := IntersectCapabilities([]JobCapabilities{a, b})
	if !result.DownloadLimit.Supported || len(result.Proxy.SupportedProtocols) != 1 || result.Proxy.SupportedProtocols[0] != "http" {
		t.Fatalf("unexpected intersection: %+v", result)
	}
}
