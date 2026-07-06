package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const rciBaseURL = "http://localhost:79/rci"

type RCIClient struct {
	baseURL string
	http    *http.Client
}

func NewRCIClient(baseURL string) *RCIClient {
	return &RCIClient{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *RCIClient) getBytes(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("rci get %s: %w", path, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rci get %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("rci get %s: read: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rci get %s: status %d", path, resp.StatusCode)
	}
	return data, nil
}

func unwrapDoubleEncodedJSON(data []byte) []byte {
	var inner string
	if json.Unmarshal(data, &inner) == nil {
		return []byte(inner)
	}
	return data
}

func (c *RCIClient) get(ctx context.Context, path string, out any) error {
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(unwrapDoubleEncodedJSON(data), out); err != nil {
		return fmt.Errorf("rci get %s: decode: %w", path, err)
	}
	return nil
}

func (c *RCIClient) post(ctx context.Context, payload any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return fmt.Errorf("rci post: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/", &buf)
	if err != nil {
		return fmt.Errorf("rci post: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("rci post: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("rci post: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rci post: status %d: %s", resp.StatusCode, data)
	}
	if msg := rciErrorMessage(data); msg != "" {
		return fmt.Errorf("rci post: %s", msg)
	}
	return nil
}

func rciErrorMessage(data []byte) string {
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	return findRCIStatusError(raw)
}

func findRCIStatusError(v any) string {
	switch val := v.(type) {
	case map[string]any:
		if s, _ := val["status"].(string); s == "error" {
			if msg, _ := val["message"].(string); msg != "" {
				return msg
			}
			return "unknown RCI error"
		}
		for _, child := range val {
			if msg := findRCIStatusError(child); msg != "" {
				return msg
			}
		}
	case []any:
		for _, item := range val {
			if msg := findRCIStatusError(item); msg != "" {
				return msg
			}
		}
	}
	return ""
}

type wansResponse struct {
	Wan struct {
		ID string `json:"id"`
	} `json:"wan"`
}

func (c *RCIClient) WANInterfaceID(ctx context.Context) (string, error) {
	var resp wansResponse
	if err := c.get(ctx, "/show/wans", &resp); err != nil {
		return "", err
	}
	if resp.Wan.ID == "" {
		return "", fmt.Errorf("router reports no WAN interface")
	}
	return resp.Wan.ID, nil
}

type interfaceState struct {
	Address string `json:"address"`
	Link    string `json:"link"`
	State   string `json:"state"`
}

func (c *RCIClient) InterfaceState(ctx context.Context, id string) (ip net.IP, up bool, err error) {
	var st interfaceState
	if err := c.get(ctx, "/show/interface/"+url.PathEscape(id), &st); err != nil {
		return nil, false, err
	}
	up = st.Link == "up" && st.State == "up"
	if st.Address != "" {
		ip = net.ParseIP(st.Address)
	}
	return ip, up, nil
}

func (c *RCIClient) SetInterfaceUp(ctx context.Context, id string, up bool) error {
	payload := map[string]any{
		"interface": map[string]any{
			id: map[string]any{"up": up},
		},
	}
	return c.post(ctx, payload)
}

func (c *RCIClient) WaitForAddress(ctx context.Context, id string, timeout time.Duration) (net.IP, error) {
	deadline := time.Now().Add(timeout)
	for {
		ip, up, err := c.InterfaceState(ctx, id)
		if err == nil && up && ip != nil {
			return ip, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("interface %s: timed out waiting for address", id)
		}
		if !sleepCtx(ctx, time.Second) {
			return nil, ctx.Err()
		}
	}
}
