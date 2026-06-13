package helperclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) Login(ctx context.Context, email, password string) (*LoginResponse, error) {
	var out LoginResponse
	err := c.request(ctx, http.MethodPost, "/api/user/login", "", map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
	}, &out)
	return &out, err
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (*LoginResponse, error) {
	var out LoginResponse
	err := c.request(ctx, http.MethodPost, "/api/user/refresh", "", map[string]string{
		"refresh_token": refreshToken,
	}, &out)
	return &out, err
}

func (c *Client) Summary(ctx context.Context, accessToken string) (*Summary, error) {
	var settings PublicSettings
	if err := c.request(ctx, http.MethodGet, "/api/public/settings", "", nil, &settings); err != nil {
		return nil, err
	}
	var profile Profile
	if err := c.request(ctx, http.MethodGet, "/api/user/profile", accessToken, nil, &profile); err != nil {
		return nil, err
	}
	var sub Subscription
	if err := c.request(ctx, http.MethodGet, "/api/user/subscription", accessToken, nil, &sub); err != nil {
		return nil, err
	}
	var keys []APIKey
	if err := c.request(ctx, http.MethodGet, "/api/user/keys", accessToken, nil, &keys); err != nil {
		return nil, err
	}
	return &Summary{
		ServerURL:     c.baseURL,
		PublicBaseURL: settings.PublicBaseURL,
		Profile:       profile,
		Subscription:  sub,
		Keys:          keys,
	}, nil
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) request(ctx context.Context, method, path, token string, body interface{}, out interface{}) error {
	if c.baseURL == "" {
		return fmt.Errorf("server URL is required")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("invalid response from %s: %s", path, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || env.Code != 0 {
		if env.Message == "" {
			env.Message = resp.Status
		}
		return errors.New(env.Message)
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return err
	}
	return nil
}
