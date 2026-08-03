package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL       string
	username      string
	password      string
	httpClient    *http.Client
	mu            sync.Mutex
	authenticated bool
}

func NewClient(baseURL, username, password string, timeout time.Duration) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: timeout,
			Jar:     jar,
		},
	}
}

func (c *Client) hasValidSessionCookie(reqURL *url.URL, respCookies []*http.Cookie) bool {
	var cookies []*http.Cookie
	if reqURL != nil && c.httpClient != nil && c.httpClient.Jar != nil {
		cookies = append(cookies, c.httpClient.Jar.Cookies(reqURL)...)
	}
	cookies = append(cookies, respCookies...)

	for _, cookie := range cookies {
		if cookie == nil || cookie.Value == "" {
			continue
		}
		if cookie.Name == "SID" || strings.HasPrefix(cookie.Name, "QBT_SID_") {
			return true
		}
	}
	return false
}

func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.authenticated = false

	data := url.Values{}
	data.Set("username", c.username)
	data.Set("password", c.password)

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v2/auth/login", strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("login failed: invalid credentials")
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("login failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("login failed to read response body: %w", err)
	}

	if !c.hasValidSessionCookie(req.URL, resp.Cookies()) {
		return errors.New("login failed: missing session cookie")
	}

	switch resp.StatusCode {
	case http.StatusOK:
		if strings.TrimSpace(string(body)) != "Ok." {
			return errors.New("login failed: invalid credentials")
		}
	case http.StatusNoContent:
		// 204 No Content is valid if a valid session cookie exists
	}

	c.authenticated = true
	return nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Referer", c.baseURL)
	return c.httpClient.Do(req)
}

func (c *Client) doAuthenticatedRequest(ctx context.Context, method, path string, bodyData []byte, contentType string) (*http.Response, error) {
	c.mu.Lock()
	auth := c.authenticated
	c.mu.Unlock()

	if !auth {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}

	var bodyReader io.Reader
	if bodyData != nil {
		bodyReader = bytes.NewReader(bodyData)
	}

	resp, err := c.doRequest(ctx, method, path, bodyReader, contentType)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		if bodyData != nil {
			bodyReader = bytes.NewReader(bodyData)
		} else {
			bodyReader = nil
		}
		resp, err = c.doRequest(ctx, method, path, bodyReader, contentType)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return nil, fmt.Errorf("authentication failed even after retry (status: %d)", resp.StatusCode)
		}
	}

	return resp, nil
}

func (c *Client) GetVersion(ctx context.Context) (string, error) {
	resp, err := c.doAuthenticatedRequest(ctx, "GET", "/api/v2/app/version", nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) GetAPIVersion(ctx context.Context) (string, error) {
	resp, err := c.doAuthenticatedRequest(ctx, "GET", "/api/v2/app/webapiVersion", nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) GetPreferences(ctx context.Context) (*qbPreferences, error) {
	resp, err := c.doAuthenticatedRequest(ctx, "GET", "/api/v2/app/preferences", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get preferences, status: %d", resp.StatusCode)
	}
	var preferences qbPreferences
	if err := json.NewDecoder(resp.Body).Decode(&preferences); err != nil {
		return nil, err
	}
	return &preferences, nil
}

func (c *Client) SetPreferences(ctx context.Context, preferences qbPreferences) error {
	payload, err := json.Marshal(preferences)
	if err != nil {
		return err
	}
	data := url.Values{"json": {string(payload)}}
	return c.postForm(ctx, "/api/v2/app/setPreferences", data)
}

// ValidateCompatibility checks if qBittorrent version is >= 5.0 and Web API version is >= 2.0.
func (c *Client) ValidateCompatibility(ctx context.Context) error {
	vStr, err := c.GetVersion(ctx)
	if err != nil {
		return fmt.Errorf("qBittorrent connection failed: %w", err)
	}

	cleanVer := strings.TrimPrefix(strings.TrimSpace(vStr), "v")
	parts := strings.Split(cleanVer, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("malformed qBittorrent version string: %q", vStr)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("malformed qBittorrent version string: %q", vStr)
	}

	if major < 5 {
		return fmt.Errorf("unsupported qBittorrent version %s: version must be >= 5.0", vStr)
	}

	apiVersion, err := c.GetAPIVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve qBittorrent Web API version: %w", err)
	}
	apiVersion = strings.TrimSpace(apiVersion)
	if apiVersion == "" {
		return fmt.Errorf("failed to retrieve qBittorrent Web API version: empty version response")
	}

	apiParts := strings.Split(apiVersion, ".")
	apiMajor, err := strconv.Atoi(apiParts[0])
	if err != nil || apiMajor < 2 {
		return fmt.Errorf("unsupported qBittorrent Web API version %s: API version must be >= 2.0", apiVersion)
	}

	return nil
}

func (c *Client) AddMagnet(ctx context.Context, magnet, savePath, category string, tags []string, stopped bool) error {
	data := url.Values{}
	data.Set("urls", magnet)
	data.Set("savepath", savePath)
	if category != "" {
		data.Set("category", category)
	}
	if len(tags) > 0 {
		data.Set("tags", strings.Join(tags, ","))
	}
	if stopped {
		data.Set("stopped", "true")
	} else {
		data.Set("stopped", "false")
		data.Set("stop_condition", "MetadataReceived")
	}

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/add", []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to add magnet, status: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) AddTorrentFile(ctx context.Context, filePath, savePath, category string, tags []string, stopped bool) error {
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	fw, err := w.CreateFormFile("torrents", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err = fw.Write(fileData); err != nil {
		return err
	}

	_ = w.WriteField("savepath", savePath)
	if category != "" {
		_ = w.WriteField("category", category)
	}
	if len(tags) > 0 {
		_ = w.WriteField("tags", strings.Join(tags, ","))
	}
	if stopped {
		_ = w.WriteField("stopped", "true")
	}

	if err = w.Close(); err != nil {
		return err
	}

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/add", b.Bytes(), w.FormDataContentType())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to add torrent file, status: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetTorrentInfo(ctx context.Context, hash string) (*qbTorrentInfo, error) {
	path := "/api/v2/torrents/info"
	if hash != "" {
		path += "?hashes=" + url.QueryEscape(hash)
	}
	resp, err := c.doAuthenticatedRequest(ctx, "GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get torrent info, status: %d", resp.StatusCode)
	}

	var infos []qbTorrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&infos); err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, errors.New("torrent not found")
	}

	return &infos[0], nil
}

func (c *Client) GetTorrents(ctx context.Context, category string) ([]qbTorrentInfo, error) {
	path := "/api/v2/torrents/info"
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	resp, err := c.doAuthenticatedRequest(ctx, "GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get torrents, status: %d", resp.StatusCode)
	}

	var infos []qbTorrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&infos); err != nil {
		return nil, err
	}
	return infos, nil
}

func (c *Client) GetTorrentFiles(ctx context.Context, hash string) ([]qbTorrentFile, error) {
	path := "/api/v2/torrents/files?hash=" + url.QueryEscape(hash)
	resp, err := c.doAuthenticatedRequest(ctx, "GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get torrent files, status: %d", resp.StatusCode)
	}

	var files []qbTorrentFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}

	return files, nil
}

func (c *Client) SetFilePriority(ctx context.Context, hash string, fileIDs []int, priority int) error {
	data := url.Values{}
	data.Set("hash", hash)

	var ids []string
	for _, id := range fileIDs {
		ids = append(ids, fmt.Sprintf("%d", id))
	}
	data.Set("id", strings.Join(ids, "|"))
	data.Set("priority", fmt.Sprintf("%d", priority))

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/filePrio", []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set file priority, status: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) postForm(ctx context.Context, path string, data url.Values) error {
	resp, err := c.doAuthenticatedRequest(ctx, "POST", path, []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		lr := io.LimitReader(resp.Body, 4096)
		bodyBytes, _ := io.ReadAll(lr)
		bodyStr := strings.TrimSpace(string(bodyBytes))
		if bodyStr != "" {
			return fmt.Errorf("%s failed with status %d: %s", path, resp.StatusCode, bodyStr)
		}
		return fmt.Errorf("%s failed with status: %d", path, resp.StatusCode)
	}
	return nil
}

func (c *Client) SetDownloadLimit(ctx context.Context, hash string, limit int64) error {
	data := url.Values{"hashes": {hash}, "limit": {strconv.FormatInt(limit, 10)}}
	return c.postForm(ctx, "/api/v2/torrents/setDownloadLimit", data)
}

func (c *Client) SetUploadLimit(ctx context.Context, hash string, limit int64) error {
	data := url.Values{"hashes": {hash}, "limit": {strconv.FormatInt(limit, 10)}}
	return c.postForm(ctx, "/api/v2/torrents/setUploadLimit", data)
}

func (c *Client) GetTorrentProperties(ctx context.Context, hash string) (*qbTorrentProperties, error) {
	path := "/api/v2/torrents/properties?hash=" + url.QueryEscape(hash)
	resp, err := c.doAuthenticatedRequest(ctx, "GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get torrent properties, status: %d", resp.StatusCode)
	}
	var properties qbTorrentProperties
	if err := json.NewDecoder(resp.Body).Decode(&properties); err != nil {
		return nil, err
	}
	return &properties, nil
}

func (c *Client) GetTrackers(ctx context.Context, hash string) ([]qbTracker, error) {
	path := "/api/v2/torrents/trackers?hash=" + url.QueryEscape(hash)
	resp, err := c.doAuthenticatedRequest(ctx, "GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get torrent trackers, status: %d", resp.StatusCode)
	}
	var trackers []qbTracker
	if err := json.NewDecoder(resp.Body).Decode(&trackers); err != nil {
		return nil, err
	}
	return trackers, nil
}

func (c *Client) AddTrackers(ctx context.Context, hash string, trackers []string) error {
	data := url.Values{"hash": {hash}, "urls": {strings.Join(trackers, "\n")}}
	return c.postForm(ctx, "/api/v2/torrents/addTrackers", data)
}

func (c *Client) SetShareLimits(ctx context.Context, hash string, ratio float64, seedingMinutes int64) error {
	data := url.Values{
		"hashes":                   {hash},
		"ratioLimit":               {strconv.FormatFloat(ratio, 'f', -1, 64)},
		"seedingTimeLimit":         {strconv.FormatInt(seedingMinutes, 10)},
		"inactiveSeedingTimeLimit": {"-1"},
		"shareLimitAction":         {"-1"},
	}
	return c.postForm(ctx, "/api/v2/torrents/setShareLimits", data)
}

func (c *Client) StartTorrents(ctx context.Context, hashes []string) error {
	data := url.Values{}
	data.Set("hashes", strings.Join(hashes, "|"))

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/start", []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to start torrents, status: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) StopTorrents(ctx context.Context, hashes []string) error {
	data := url.Values{}
	data.Set("hashes", strings.Join(hashes, "|"))

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/stop", []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to stop torrents, status: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) DeleteTorrents(ctx context.Context, hashes []string, deleteFiles bool) error {
	data := url.Values{}
	data.Set("hashes", strings.Join(hashes, "|"))
	if deleteFiles {
		data.Set("deleteFiles", "true")
	} else {
		data.Set("deleteFiles", "false")
	}

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/delete", []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete torrents, status: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) CreateCategory(ctx context.Context, category, savePath string) error {
	data := url.Values{}
	data.Set("category", category)
	if savePath != "" {
		data.Set("savePath", savePath)
	}

	resp, err := c.doAuthenticatedRequest(ctx, "POST", "/api/v2/torrents/createCategory", []byte(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to create category, status: %d", resp.StatusCode)
	}
	return nil
}
