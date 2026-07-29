package networkpolicy

// ProjectCapabilities turns an adapter declaration into the normalized public
// shape consumed by clients. Callers decide whether a job is currently mutable.
func ProjectCapabilities(c EngineCapabilities, mutable bool) JobCapabilities {
	state := func(supported bool, field string) CapabilityState {
		startupOnly := c.StartupOnly[field]
		result := CapabilityState{Supported: supported, MutableNow: supported && mutable && !startupOnly, StartupOnly: startupOnly}
		if !supported {
			result.Reason = "unsupported by the selected download strategy"
		} else if startupOnly {
			result.Reason = "applied when a new engine process starts"
		} else if !mutable {
			result.Reason = "not mutable in the current job state"
		}
		return result
	}
	proxyProtocols := make([]string, 0, len(c.ProxyProtocols))
	for _, protocol := range c.ProxyProtocols {
		proxyProtocols = append(proxyProtocols, string(protocol))
	}
	result := JobCapabilities{
		Pause: state(c.Pause, "pause"), Resume: state(c.Resume, "resume"),
		Cancel: state(c.Cancel, "cancel"), Retry: state(c.Retry, "retry"),
		DownloadLimit: state(c.PerJobDownloadLimit, "downloadLimit"),
		UploadLimit:   state(c.PerJobUploadLimit, "uploadLimit"),
		Proxy:         state(c.Proxy, "proxy"), UserAgent: state(c.UserAgent, "userAgent"),
		CustomHeaders: state(c.CustomHeaders, "customHeaders"),
		RetryPolicy:   state(c.RetryPolicy, "retryPolicy"),
		TimeoutPolicy: state(c.TimeoutPolicy, "timeoutPolicy"),
		Connections:   state(c.Connections, "connections"),
		FileSelection: state(c.FileSelection, "fileSelection"),
		Trackers:      state(c.Trackers, "trackers"),
		SeedingPolicy: state(c.SeedingPolicy, "seedingPolicy"),
	}
	result.Proxy.SupportedProtocols = proxyProtocols
	return result
}

// IntersectCapabilities returns controls supported by every selected source.
func IntersectCapabilities(items []JobCapabilities) JobCapabilities {
	if len(items) == 0 {
		return JobCapabilities{}
	}
	result := items[0]
	for _, item := range items[1:] {
		intersectState(&result.Pause, item.Pause)
		intersectState(&result.Resume, item.Resume)
		intersectState(&result.Cancel, item.Cancel)
		intersectState(&result.Retry, item.Retry)
		intersectState(&result.DownloadLimit, item.DownloadLimit)
		intersectState(&result.UploadLimit, item.UploadLimit)
		intersectState(&result.Proxy, item.Proxy)
		intersectState(&result.UserAgent, item.UserAgent)
		intersectState(&result.CustomHeaders, item.CustomHeaders)
		intersectState(&result.RetryPolicy, item.RetryPolicy)
		intersectState(&result.TimeoutPolicy, item.TimeoutPolicy)
		intersectState(&result.Connections, item.Connections)
		intersectState(&result.FileSelection, item.FileSelection)
		intersectState(&result.Trackers, item.Trackers)
		intersectState(&result.SeedingPolicy, item.SeedingPolicy)
	}
	return result
}

func intersectState(dst *CapabilityState, other CapabilityState) {
	dst.Supported = dst.Supported && other.Supported
	dst.MutableNow = dst.MutableNow && other.MutableNow
	dst.StartupOnly = dst.StartupOnly || other.StartupOnly
	if !dst.Supported {
		dst.Reason = "not supported by every selected source"
	}
	if len(dst.SupportedProtocols) > 0 {
		allowed := make(map[string]bool, len(other.SupportedProtocols))
		for _, value := range other.SupportedProtocols {
			allowed[value] = true
		}
		values := dst.SupportedProtocols[:0]
		for _, value := range dst.SupportedProtocols {
			if allowed[value] {
				values = append(values, value)
			}
		}
		dst.SupportedProtocols = values
	}
}
