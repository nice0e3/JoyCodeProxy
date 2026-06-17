package joycode

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	// Color gateway (api-ai.jd.com) — all authenticated API calls go through this.
	ColorGateway = "https://api-ai.jd.com"
	ColorAPIPath = "/api"
	ColorSecret  = "0691a3f0b37b4a85aeb63ad0fc7db3ed"
	ColorAppID   = "joycode_ide"

	// Color gateway function IDs.
	FnUserInfo     = "joycode_userInfo"
	FnModelList    = "joycode_modelList"
	FnChatComplete = "chat_completions"
	FnWebSearch    = "joycode_webSearch"
	FnRerank       = "joycode_rerank"

	DefaultModel = "JoyAI-Code"
	UserAgent    = "node"
)

var Models = []string{
	"JoyAI-Code",
	"Claude-Opus-4.7",
	"MiniMax-M2.7",
	"Kimi-K2.6",
	"Kimi-K2.5",
	"GLM-5.1",
	"GLM-5",
	"GLM-4.7",
	"Doubao-Seed-2.0-pro",
}

// ColorGatewayURL builds a signed URL for the JoyCode color gateway.
// Params are sorted by key, values joined with "&", then HMAC-SHA256 signed.
func ColorGatewayURL(functionId string) string {
	t := time.Now().UnixMilli()
	params := map[string]string{
		"appid":      ColorAppID,
		"functionId": functionId,
		"t":          fmt.Sprintf("%d", t),
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var vals []string
	for _, k := range keys {
		if v := params[k]; v != "" {
			vals = append(vals, v)
		}
	}
	signStr := strings.Join(vals, "&")

	mac := hmac.New(sha256.New, []byte(ColorSecret))
	mac.Write([]byte(signStr))
	sign := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s%s?appid=%s&functionId=%s&t=%d&sign=%s",
		ColorGateway, ColorAPIPath, ColorAppID, functionId, t, sign)
}

// endpointToFunctionID maps legacy endpoint paths to color gateway function IDs.
func endpointToFunctionID(endpoint string) string {
	switch endpoint {
	case "/api/saas/openai/v1/chat/completions":
		return FnChatComplete
	case "/api/saas/anthropic/v1/messages":
		return FnChatComplete // unified gateway
	case "/api/saas/models/v1/modelList":
		return FnModelList
	case "/api/saas/user/v1/userInfo":
		return FnUserInfo
	case "/api/saas/openai/v1/web-search":
		return FnWebSearch
	case "/api/saas/openai/v1/rerank":
		return FnRerank
	default:
		return strings.TrimPrefix(endpoint, "/api/")
	}
}

type Client struct {
	PtKey          string
	AnthropicPtKey string
	UserID         string
	Tenant         string
	httpClient     *http.Client
}

type gzipReadCloser struct {
	io.Reader
	body io.Closer
	gzip io.Closer
}

func (r *gzipReadCloser) Close() error {
	gzipErr := r.gzip.Close()
	bodyErr := r.body.Close()
	if gzipErr != nil {
		return gzipErr
	}
	return bodyErr
}

func NewClient(ptKey, userID string) *Client {
	return &Client{
		PtKey:      ptKey,
		UserID:     userID,
		Tenant:     "JD",
		httpClient: &http.Client{Timeout: 30 * time.Minute},
	}
}

// SetHTTPClient replaces the internal HTTP client. Intended for testing.
func (c *Client) SetHTTPClient(hc *http.Client) {
	c.httpClient = hc
}

func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

func (c *Client) SetTransport(transport http.RoundTripper) {
	c.httpClient.Transport = transport
}

func (c *Client) SetAnthropicPtKey(ptKey string) {
	c.AnthropicPtKey = ptKey
}

func newHexID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Client) headers() http.Header {
	h := http.Header{
		"Content-Type":    {"application/json; charset=UTF-8"},
		"ptKey":           {c.PtKey},
		"loginType":       {"PIN_JD_CLOUD"},
		"tenant":          {c.Tenant},
		"User-Agent":      {UserAgent},
		"Accept":          {"*/*"},
		"Accept-Encoding": {"gzip, deflate, br"},
		"Connection":      {"keep-alive"},
	}
	return h
}

func (c *Client) anthropicHeaders() http.Header {
	ptKey := c.PtKey
	if c.AnthropicPtKey != "" {
		ptKey = c.AnthropicPtKey
	}
	return http.Header{
		"Content-Type":    {"application/json; charset=utf-8"},
		"ptKey":           {ptKey},
		"loginType":       {"PIN_JD_CLOUD"},
		"tenant":          {c.Tenant},
		"User-Agent":      {UserAgent},
		"Accept":          {"*/*"},
		"Accept-Encoding": {"gzip, deflate, br"},
		"Connection":      {"keep-alive"},
	}
}

func (c *Client) doPost(endpoint string, body map[string]interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		slog.Error("marshal request body", "endpoint", endpoint, "error", err)
		return nil, err
	}
	functionID := endpointToFunctionID(endpoint)
	url := ColorGatewayURL(functionID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		slog.Error("create request", "endpoint", endpoint, "error", err)
		return nil, err
	}
	req.Header = c.headers()
	return c.httpClient.Do(req)
}

func (c *Client) doAnthropicPost(endpoint string, body map[string]interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		slog.Error("marshal anthropic request body", "endpoint", endpoint, "error", err)
		return nil, err
	}
	// Anthropic native endpoint also uses the unified color gateway
	url := ColorGatewayURL(FnChatComplete)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		slog.Error("create anthropic request", "endpoint", endpoint, "error", err)
		return nil, err
	}
	req.Header = c.anthropicHeaders()
	return c.httpClient.Do(req)
}

func decodeBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var r io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	return io.ReadAll(r)
}

func decodeStreamBody(resp *http.Response) error {
	if resp.Header.Get("Content-Encoding") != "gzip" {
		return nil
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	resp.Body = &gzipReadCloser{Reader: gz, body: resp.Body, gzip: gz}
	resp.Header.Del("Content-Encoding")
	return nil
}

func (c *Client) Post(endpoint string, body map[string]interface{}) (map[string]interface{}, error) {
	resp, err := c.doPost(endpoint, body)
	if err != nil {
		slog.Error("upstream request failed", "endpoint", endpoint, "error", err)
		return nil, err
	}
	data, err := decodeBody(resp)
	if err != nil {
		slog.Error("decode upstream response", "endpoint", endpoint, "status", resp.StatusCode, "error", err)
		return nil, err
	}
	// Color gateway may return status_code 0 as success
	if resp.StatusCode != 0 && resp.StatusCode != 200 {
		slog.Error("upstream non-200", "endpoint", endpoint, "status", resp.StatusCode, "body", truncate(string(data), 500))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		slog.Error("unmarshal upstream response", "endpoint", endpoint, "error", err)
		return nil, fmt.Errorf("invalid JSON response (parse error: %s): %s", err.Error(), truncate(string(data), 500))
	}
	return result, nil
}

func (c *Client) PostStream(endpoint string, body map[string]interface{}) (*http.Response, error) {
	resp, err := c.doPost(endpoint, body)
	if err != nil {
		slog.Error("upstream stream connect", "endpoint", endpoint, "error", err)
		return nil, err
	}
	// Color gateway may return status_code 0 as success
	if resp.StatusCode != 0 && resp.StatusCode != 200 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		slog.Error("upstream stream non-200", "endpoint", endpoint, "status", resp.StatusCode, "body", truncate(string(data), 500))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	if err := decodeStreamBody(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func (c *Client) PostAnthropicStream(endpoint string, body map[string]interface{}) (*http.Response, error) {
	resp, err := c.doAnthropicPost(endpoint, body)
	if err != nil {
		slog.Error("upstream anthropic stream connect", "endpoint", endpoint, "error", err)
		return nil, err
	}
	if resp.StatusCode != 0 && resp.StatusCode != 200 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		slog.Error("upstream anthropic stream non-200", "endpoint", endpoint, "status", resp.StatusCode, "body", truncate(string(data), 500))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	if err := decodeStreamBody(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func (c *Client) ListModels() ([]ModelInfo, error) {
	resp, err := c.Post("/api/saas/models/v1/modelList", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected models response format: missing data array")
	}
	models := make([]ModelInfo, 0, len(data))
	for _, item := range data {
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var m ModelInfo
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		models = append(models, m)
	}
	return models, nil
}

func (c *Client) WebSearch(query string) ([]interface{}, error) {
	body := map[string]interface{}{
		"messages": []map[string]string{{"role": "user", "content": query}},
		"stream": false, "model": "search_pro_jina", "language": "UNKNOWN",
	}
	resp, err := c.Post("/api/saas/openai/v1/web-search", body)
	if err != nil {
		return nil, err
	}
	results, _ := resp["search_result"].([]interface{})
	return results, nil
}

func (c *Client) Rerank(query string, documents []string, topN int) (map[string]interface{}, error) {
	return c.Post("/api/saas/openai/v1/rerank", map[string]interface{}{
		"model": "Qwen3-Reranker-8B", "query": query,
		"documents": documents, "top_n": topN,
	})
}

func (c *Client) UserInfo() (map[string]interface{}, error) {
	return c.Post("/api/saas/user/v1/userInfo", map[string]interface{}{})
}

func (c *Client) Validate() error {
	resp, err := c.UserInfo()
	if err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}
	code, ok := resp["code"].(float64)
	if !ok || code != 0 {
		msg, _ := resp["msg"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("credential validation failed (code=%.0f): %s", code, msg)
	}
	return nil
}

// UserInfoWithRefresh calls the UserInfo API and returns the refreshed ptKey
// from the response data, if present. Returns (refreshedPtKey, nil) on success.
func (c *Client) UserInfoWithRefresh() (string, error) {
	resp, err := c.UserInfo()
	if err != nil {
		return "", fmt.Errorf("user info request failed: %w", err)
	}
	code, ok := resp["code"].(float64)
	if !ok || code != 0 {
		msg, _ := resp["msg"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		return "", fmt.Errorf("user info failed (code=%.0f): %s", code, msg)
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	if ptKey, ok := data["ptKey"].(string); ok && ptKey != "" {
		return ptKey, nil
	}
	return "", nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
