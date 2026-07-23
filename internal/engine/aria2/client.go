package aria2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
)

// Client communicates with aria2c via JSON-RPC.
type Client struct {
	rpcURL string
	secret string
	idSeq  atomic.Int64
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// StatusInfo holds the raw aria2 status response fields.
type StatusInfo struct {
	GID             string     `json:"gid"`
	Status          string     `json:"status"`
	TotalLength     string     `json:"totalLength"`
	CompletedLength string     `json:"completedLength"`
	DownloadSpeed   string     `json:"downloadSpeed"`
	ErrorCode       string     `json:"errorCode,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	Files           []FileInfo `json:"files,omitempty"`
}

// FileInfo holds file information from aria2.
type FileInfo struct {
	Path string    `json:"path"`
	URIs []URIInfo `json:"uris"`
}

// URIInfo holds URI information from aria2.
type URIInfo struct {
	URI    string `json:"uri"`
	Status string `json:"status"`
}

// NewClient creates a new aria2 JSON-RPC client.
func NewClient(rpcURL, secret string) *Client {
	return &Client{
		rpcURL: rpcURL,
		secret: secret,
	}
}

func (c *Client) call(method string, params ...interface{}) (json.RawMessage, error) {
	id := fmt.Sprintf("dm-%d", c.idSeq.Add(1))

	allParams := make([]interface{}, 0, len(params)+1)
	if c.secret != "" {
		allParams = append(allParams, "token:"+c.secret)
	}
	allParams = append(allParams, params...)

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  allParams,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := http.Post(c.rpcURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rpc call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("aria2 error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// AddURI sends a download request to aria2 and returns the GID.
func (c *Client) AddURI(uri string, dir string) (string, error) {
	uris := []string{uri}
	opts := map[string]string{
		"dir": dir,
	}

	result, err := c.call("aria2.addUri", uris, opts)
	if err != nil {
		return "", err
	}

	var gid string
	if err := json.Unmarshal(result, &gid); err != nil {
		return "", fmt.Errorf("unmarshal gid: %w", err)
	}

	return gid, nil
}

// TellStatus retrieves the status of a download by its GID.
func (c *Client) TellStatus(gid string) (*StatusInfo, error) {
	result, err := c.call("aria2.tellStatus", gid)
	if err != nil {
		return nil, err
	}

	var info StatusInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}

	return &info, nil
}

// Remove cancels a download.
func (c *Client) Remove(gid string) error {
	_, err := c.call("aria2.remove", gid)
	return err
}

// ForceRemove force-cancels a download.
func (c *Client) ForceRemove(gid string) error {
	_, err := c.call("aria2.forceRemove", gid)
	return err
}

// Pause pauses an active download.
func (c *Client) Pause(gid string) error {
	_, err := c.call("aria2.pause", gid)
	return err
}

// Unpause resumes a paused download.
func (c *Client) Unpause(gid string) error {
	_, err := c.call("aria2.unpause", gid)
	return err
}

// TellActive returns status of all active downloads.
func (c *Client) TellActive() ([]StatusInfo, error) {
	result, err := c.call("aria2.tellActive")
	if err != nil {
		return nil, err
	}
	var infos []StatusInfo
	if err := json.Unmarshal(result, &infos); err != nil {
		return nil, fmt.Errorf("unmarshal active list: %w", err)
	}
	return infos, nil
}

// TellWaiting returns status of waiting downloads.
func (c *Client) TellWaiting(offset, num int) ([]StatusInfo, error) {
	result, err := c.call("aria2.tellWaiting", offset, num)
	if err != nil {
		return nil, err
	}
	var infos []StatusInfo
	if err := json.Unmarshal(result, &infos); err != nil {
		return nil, fmt.Errorf("unmarshal waiting list: %w", err)
	}
	return infos, nil
}

// TellStopped returns status of stopped downloads.
func (c *Client) TellStopped(offset, num int) ([]StatusInfo, error) {
	result, err := c.call("aria2.tellStopped", offset, num)
	if err != nil {
		return nil, err
	}
	var infos []StatusInfo
	if err := json.Unmarshal(result, &infos); err != nil {
		return nil, fmt.Errorf("unmarshal stopped list: %w", err)
	}
	return infos, nil
}

// ParseInt64 converts aria2 string numbers to int64.
func ParseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
