package toolmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Fixed upstream release source. Requests never use repositories, URLs, or channels supplied by callers.
const (
	ytdlpReleaseRepo     = "yt-dlp/yt-dlp"
	githubAPIBase        = "https://api.github.com"
	ytdlpManifestAsset   = "SHA2-256SUMS"
	ytdlpSignatureAsset  = "SHA2-256SUMS.sig"
	acquisitionUserAgent = "GoDownloader-ToolManager"
)

// Download bounds. Observed for release 2026.10.26: metadata ~60 KiB, SHA2-256SUMS 1595 B,
// SHA2-256SUMS.sig 566 B, yt-dlp.exe 23.6 MB.
const (
	maxReleaseMetadataBytes = 1 << 20
	maxExecutableBytes      = 64 << 20
	maxRedirectHops         = 3
)

// defaultAllowedHosts is the exact HTTPS host allowlist. The release CDN host was verified against live 302
// responses from the asset API; no wildcard *.githubusercontent.com is allowed.
var defaultAllowedHosts = map[string]bool{
	"api.github.com":                       true,
	"release-assets.githubusercontent.com": true,
}

// releaseAsset identifies one asset of the fixed upstream release by numeric ID.
type releaseAsset struct {
	ID   int64
	Name string
	Size int64
}

// ytdlpRelease is the subset of stable release metadata GoDownloader trusts structurally (not cryptographically).
type ytdlpRelease struct {
	Tag       string
	Exe       releaseAsset
	Manifest  releaseAsset
	Signature releaseAsset
}

// releaseClient is a dedicated, bounded HTTPS client for official yt-dlp release assets.
type releaseClient struct {
	apiBase      string
	allowedHosts map[string]bool
	requireHTTPS bool
	client       *http.Client
	metaTimeout  time.Duration
	smallTimeout time.Duration
	exeTimeout   time.Duration
}

func newReleaseClient(transport http.RoundTripper) *releaseClient {
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	rc := &releaseClient{
		apiBase:      githubAPIBase,
		allowedHosts: defaultAllowedHosts,
		requireHTTPS: true,
		metaTimeout:  30 * time.Second,
		smallTimeout: 60 * time.Second,
		exeTimeout:   10 * time.Minute,
	}
	rc.client = &http.Client{Transport: transport, CheckRedirect: rc.checkRedirect}
	return rc
}

// checkRedirect enforces hop count, HTTPS, and the exact host allowlist on every redirect, and strips any
// credential-bearing header so nothing sensitive reaches the release CDN.
func (c *releaseClient) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirectHops {
		return fmt.Errorf("%w: too many redirects", ErrAcquisitionNetwork)
	}
	if err := c.checkURL(req.URL); err != nil {
		return err
	}
	req.Header.Del("Authorization")
	req.Header.Del("Cookie")
	req.Header.Del("Proxy-Authorization")
	return nil
}

func (c *releaseClient) checkURL(u *url.URL) error {
	if c.requireHTTPS && u.Scheme != "https" {
		return fmt.Errorf("%w: refusing non-HTTPS request to %s", ErrAcquisitionNetwork, u.Hostname())
	}
	if !c.allowedHosts[u.Hostname()] {
		return fmt.Errorf("%w: host %s is not an allowed release host", ErrAcquisitionNetwork, u.Hostname())
	}
	return nil
}

// sanitizeNetError drops the URL (which may carry signed CDN query tokens) from transport errors and keeps
// only a bounded category plus the host.
func sanitizeNetError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: cancelled", ErrAcquisitionNetwork)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: timed out", ErrAcquisitionNetwork)
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if parsed, perr := url.Parse(uerr.URL); perr == nil {
			host = parsed.Hostname()
		}
		inner := uerr.Err
		if errors.Is(inner, ErrAcquisitionNetwork) {
			return inner
		}
		if uerr.Timeout() {
			return fmt.Errorf("%w: request to %s timed out", ErrAcquisitionNetwork, host)
		}
		var nerr net.Error
		if errors.As(inner, &nerr) {
			return fmt.Errorf("%w: connection to %s failed", ErrAcquisitionNetwork, host)
		}
		return fmt.Errorf("%w: request to %s failed", ErrAcquisitionNetwork, host)
	}
	return fmt.Errorf("%w: request failed", ErrAcquisitionNetwork)
}

func (c *releaseClient) get(ctx context.Context, path, accept string) (*http.Response, error) {
	u, err := url.Parse(c.apiBase + path)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request URL", ErrAcquisitionNetwork)
	}
	if err := c.checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request", ErrAcquisitionNetwork)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", acquisitionUserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, sanitizeNetError(err)
	}
	if resp.StatusCode != http.StatusOK {
		host := resp.Request.URL.Hostname()
		resp.Body.Close()
		return nil, fmt.Errorf("%w: unexpected HTTP %d from %s", ErrAcquisitionNetwork, resp.StatusCode, host)
	}
	return resp, nil
}

// readBounded reads at most max bytes and fails if the body is larger.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, sanitizeNetError(err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrAcquisitionNetwork, max)
	}
	return data, nil
}

type githubReleaseJSON struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// latestRelease reads the official stable release metadata. The metadata only selects asset IDs and the
// expected version; it is not proof of authenticity (the signed manifest is).
func (c *releaseClient) latestRelease(ctx context.Context) (*ytdlpRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, c.metaTimeout)
	defer cancel()
	resp, err := c.get(ctx, "/repos/"+ytdlpReleaseRepo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, maxReleaseMetadataBytes)
	if err != nil {
		return nil, err
	}
	var meta githubReleaseJSON
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("%w: release metadata is not valid JSON", ErrInvalidRelease)
	}
	if meta.Draft || meta.Prerelease {
		return nil, fmt.Errorf("%w: latest release is not a stable release", ErrInvalidRelease)
	}
	if _, err := parseYtdlpVersion(meta.TagName); err != nil {
		return nil, err
	}
	rel := &ytdlpRelease{Tag: meta.TagName}
	want := map[string]*releaseAsset{
		ytdlpWindowsAsset:   &rel.Exe,
		ytdlpManifestAsset:  &rel.Manifest,
		ytdlpSignatureAsset: &rel.Signature,
	}
	seen := map[string]int{}
	for _, a := range meta.Assets {
		target, ok := want[a.Name]
		if !ok {
			continue
		}
		seen[a.Name]++
		*target = releaseAsset{ID: a.ID, Name: a.Name, Size: a.Size}
	}
	for name := range want {
		if seen[name] != 1 {
			return nil, fmt.Errorf("%w: release must contain exactly one %s asset", ErrInvalidRelease, name)
		}
	}
	for _, a := range []releaseAsset{rel.Manifest, rel.Signature} {
		if a.ID <= 0 || a.Size <= 0 || a.Size > maxManifestBytes {
			return nil, fmt.Errorf("%w: %s asset size is outside accepted bounds", ErrInvalidRelease, a.Name)
		}
	}
	if rel.Exe.ID <= 0 || rel.Exe.Size <= 0 || rel.Exe.Size > maxExecutableBytes {
		return nil, fmt.Errorf("%w: executable asset size is outside accepted bounds", ErrInvalidRelease)
	}
	return rel, nil
}

func assetPath(a releaseAsset) string {
	return "/repos/" + ytdlpReleaseRepo + "/releases/assets/" + strconv.FormatInt(a.ID, 10)
}

// fetchSmallAsset downloads a small text asset (manifest or signature) fully into memory, bounded.
func (c *releaseClient) fetchSmallAsset(ctx context.Context, a releaseAsset, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.smallTimeout)
	defer cancel()
	resp, err := c.get(ctx, assetPath(a), "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := readBounded(resp.Body, max)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != a.Size {
		return nil, fmt.Errorf("%w: %s size %d does not match release metadata", ErrAcquisitionNetwork, a.Name, len(data))
	}
	return data, nil
}

// streamAsset streams the executable asset into w without buffering it in memory, bounded by max and by the
// size declared in release metadata. Truncated or oversized bodies fail.
func (c *releaseClient) streamAsset(ctx context.Context, a releaseAsset, w io.Writer, max int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.exeTimeout)
	defer cancel()
	resp, err := c.get(ctx, assetPath(a), "application/octet-stream")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > max {
		return 0, fmt.Errorf("%w: executable exceeds %d bytes", ErrAcquisitionNetwork, max)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return n, sanitizeNetError(err)
	}
	if n > max {
		return n, fmt.Errorf("%w: executable exceeds %d bytes", ErrAcquisitionNetwork, max)
	}
	if n != a.Size {
		return n, fmt.Errorf("%w: executable download truncated (%d of %d bytes)", ErrAcquisitionNetwork, n, a.Size)
	}
	return n, nil
}
