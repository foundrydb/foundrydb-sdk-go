// Package foundrydb provides a Go client for the FoundryDB managed database platform.
//
// # Basic usage
//
//	client := foundrydb.New(foundrydb.Config{
//	    APIURL:   "https://api.foundrydb.com",
//	    Username: "admin",
//	    Password: "yourpassword",
//	})
//
//	services, err := client.ListServices(context.Background())
//
// # Bearer token authentication
//
//	client := foundrydb.New(foundrydb.Config{
//	    APIURL: "https://api.foundrydb.com",
//	    Token:  "your-bearer-token",
//	})
//
// # Organization scoping
//
// Set OrgID on the Config to scope every request to a specific organization:
//
//	client := foundrydb.New(foundrydb.Config{
//	    Username: "admin",
//	    Password: "pass",
//	    OrgID:    "org-uuid",
//	})
//
// The OrgID is sent via the X-Active-Org-ID header on every API call.
package foundrydb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAPIURL = "https://api.foundrydb.com"

// Config holds the configuration for a FoundryDB client.
type Config struct {
	// APIURL is the base URL of the FoundryDB API.
	// Defaults to "https://api.foundrydb.com" when empty.
	APIURL string

	// Username and Password are used for HTTP Basic Auth.
	// Either Username+Password or Token must be provided.
	Username string
	Password string

	// Token is a Bearer token used for authentication.
	// Takes precedence over Username+Password when set.
	Token string

	// OrgID is an optional organization UUID sent as X-Active-Org-ID on every request.
	OrgID string

	// HTTPTimeout is the timeout for individual HTTP requests.
	// Defaults to 30 seconds when zero.
	HTTPTimeout time.Duration
}

// Client is the main FoundryDB API client.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a new FoundryDB client with the given configuration.
func New(cfg Config) *Client {
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")

	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// do executes an HTTP request against the API. orgID overrides the client-level OrgID when non-empty.
func (c *Client) do(ctx context.Context, method, path string, body interface{}, orgID string) (*http.Response, error) {
	var reqBody io.Reader
	contentType := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("foundrydb: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
		contentType = "application/json"
	}
	return c.doRaw(ctx, method, path, reqBody, contentType, orgID)
}

// doRaw executes an HTTP request whose body is sent verbatim rather than
// JSON-encoded, for the endpoints that take an opaque binary payload (a gzipped
// source tarball). It shares every other concern with do: authentication, the
// active-org header, and error wrapping. Pass a nil body, and an empty
// contentType, for a request that has neither.
func (c *Client) doRaw(ctx context.Context, method, path string, body io.Reader, contentType, orgID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.APIURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("foundrydb: create request: %w", err)
	}

	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	} else {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")

	// Resolve active org: call-level override takes precedence over client-level.
	activeOrg := orgID
	if activeOrg == "" {
		activeOrg = c.cfg.OrgID
	}
	if activeOrg != "" {
		req.Header.Set("X-Active-Org-ID", activeOrg)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("foundrydb: request %s %s: %w", method, path, err)
	}
	return resp, nil
}

// checkResponse reads the body and returns an APIError when the status code is not 2xx.
// On success it returns the raw body bytes for the caller to decode.
func checkResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("foundrydb: read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := extractErrorMessage(data)
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    msg,
			Body:       string(data),
		}
	}
	return data, nil
}

// extractErrorMessage attempts to pull a human-readable message out of a JSON error body.
func extractErrorMessage(body []byte) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if payload.Error != "" {
			return payload.Error
		}
		if payload.Message != "" {
			return payload.Message
		}
	}
	raw := strings.TrimSpace(string(body))
	if len(raw) > 200 {
		raw = raw[:200] + "..."
	}
	return raw
}
