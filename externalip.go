package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var defaultCheckURLs = []string{
	"https://api.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://checkip.amazonaws.com",
}

type IPChecker struct {
	http *http.Client
}

func NewIPChecker() *IPChecker {
	return &IPChecker{
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *IPChecker) External(ctx context.Context, urls []string) (net.IP, error) {
	var lastErr error
	for _, u := range urls {
		ip, err := c.fetch(ctx, u)
		if err == nil {
			return ip, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all IP check services failed, last error: %w", lastErr)
}

func (c *IPChecker) fetch(ctx context.Context, rawURL string) (net.IP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(data))
	ip := net.ParseIP(text)
	if ip == nil {
		return nil, fmt.Errorf("%s: invalid IP response %q", rawURL, text)
	}
	return ip, nil
}
