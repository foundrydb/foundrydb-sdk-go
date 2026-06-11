package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// WebhookEndpoint is a customer-configured HTTP endpoint that receives signed
// event notifications. The signing secret is returned only on creation.
type WebhookEndpoint struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Active    bool      `json:"active"`
	Secret    string    `json:"secret,omitempty"` // populated only in the create response
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Delivery health.
	ConsecutiveFailures int        `json:"consecutive_failures"`
	TotalDelivered      int64      `json:"total_delivered"`
	TotalFailed         int64      `json:"total_failed"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
	DisabledAt          *time.Time `json:"disabled_at,omitempty"`
	DisabledReason      *string    `json:"disabled_reason,omitempty"`
}

// WebhookDelivery is one entry in a webhook endpoint's delivery queue/history.
type WebhookDelivery struct {
	ID             string     `json:"id"`
	WebhookID      string     `json:"webhook_id"`
	EventID        *string    `json:"event_id,omitempty"`
	EventType      string     `json:"event_type"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	ResponseStatus *int       `json:"response_status,omitempty"`
	ResponseBody   *string    `json:"response_body,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	FailedAt       *time.Time `json:"failed_at,omitempty"`
	ErrorMessage   *string    `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreateWebhookRequest is the request body for registering a webhook endpoint.
// An empty Events list subscribes the endpoint to every event type.
type CreateWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// Event is one entry in the queryable event stream returned by ListEvents.
type Event struct {
	Seq            int64           `json:"seq"`
	ID             string          `json:"id"`
	OrganizationID *string         `json:"organization_id,omitempty"`
	ServiceID      *string         `json:"service_id,omitempty"`
	EventType      string          `json:"event_type"`
	Data           json.RawMessage `json:"data"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ListEventsOptions controls pagination and filtering for ListEvents.
type ListEventsOptions struct {
	// Cursor is the next_cursor value from a previous page; zero starts at the newest event.
	Cursor int64
	// Limit caps the page size (server default 50, maximum 200).
	Limit int
	// EventType filters the feed to a single event type when non-empty.
	EventType string
}

// ListEventsResponse is one page of the event feed.
type ListEventsResponse struct {
	Events     []Event `json:"events"`
	NextCursor *int64  `json:"next_cursor,omitempty"`
}

type listWebhooksResponse struct {
	Webhooks []WebhookEndpoint `json:"webhooks"`
}

type listWebhookDeliveriesResponse struct {
	Deliveries []WebhookDelivery `json:"deliveries"`
}

func orgWebhooksPath(orgID string) string {
	return "/organizations/" + orgID + "/webhooks"
}

// CreateOrgWebhook registers a webhook endpoint for an organization.
// The returned endpoint includes the signing secret exactly once.
func (c *Client) CreateOrgWebhook(ctx context.Context, orgID string, req CreateWebhookRequest) (*WebhookEndpoint, error) {
	resp, err := c.do(ctx, http.MethodPost, orgWebhooksPath(orgID), req, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var endpoint WebhookEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateOrgWebhook response: %w", err)
	}
	return &endpoint, nil
}

// ListOrgWebhooks returns all webhook endpoints of an organization.
func (c *Client) ListOrgWebhooks(ctx context.Context, orgID string) ([]WebhookEndpoint, error) {
	resp, err := c.do(ctx, http.MethodGet, orgWebhooksPath(orgID), nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listWebhooksResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListOrgWebhooks response: %w", err)
	}
	return result.Webhooks, nil
}

// GetOrgWebhook returns one webhook endpoint of an organization.
// Returns nil, nil when the endpoint does not exist (404).
func (c *Client) GetOrgWebhook(ctx context.Context, orgID, webhookID string) (*WebhookEndpoint, error) {
	resp, err := c.do(ctx, http.MethodGet, orgWebhooksPath(orgID)+"/"+webhookID, nil, orgID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, nil
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var endpoint WebhookEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetOrgWebhook response: %w", err)
	}
	return &endpoint, nil
}

// DeleteOrgWebhook removes a webhook endpoint from an organization.
func (c *Client) DeleteOrgWebhook(ctx context.Context, orgID, webhookID string) error {
	resp, err := c.do(ctx, http.MethodDelete, orgWebhooksPath(orgID)+"/"+webhookID, nil, orgID)
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// TestOrgWebhook enqueues a test event delivery for a webhook endpoint,
// bypassing its event-type filter.
func (c *Client) TestOrgWebhook(ctx context.Context, orgID, webhookID string) error {
	resp, err := c.do(ctx, http.MethodPost, orgWebhooksPath(orgID)+"/"+webhookID+"/test", nil, orgID)
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// ListOrgWebhookDeliveries returns the most recent deliveries for a webhook endpoint.
func (c *Client) ListOrgWebhookDeliveries(ctx context.Context, orgID, webhookID string) ([]WebhookDelivery, error) {
	resp, err := c.do(ctx, http.MethodGet, orgWebhooksPath(orgID)+"/"+webhookID+"/deliveries", nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listWebhookDeliveriesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListOrgWebhookDeliveries response: %w", err)
	}
	return result.Deliveries, nil
}

// ReplayOrgWebhookDelivery enqueues a fresh delivery re-sending the payload of
// a prior delivery and returns the new delivery record.
func (c *Client) ReplayOrgWebhookDelivery(ctx context.Context, orgID, webhookID, deliveryID string) (*WebhookDelivery, error) {
	path := orgWebhooksPath(orgID) + "/" + webhookID + "/deliveries/" + deliveryID + "/replay"
	resp, err := c.do(ctx, http.MethodPost, path, nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var delivery WebhookDelivery
	if err := json.Unmarshal(data, &delivery); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ReplayOrgWebhookDelivery response: %w", err)
	}
	return &delivery, nil
}

// ListEvents returns one page of the cursor-paginated event feed visible to
// the authenticated user (own events plus organization memberships).
func (c *Client) ListEvents(ctx context.Context, opts ListEventsOptions) (*ListEventsResponse, error) {
	q := url.Values{}
	if opts.Cursor > 0 {
		q.Set("cursor", strconv.FormatInt(opts.Cursor, 10))
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.EventType != "" {
		q.Set("event_type", opts.EventType)
	}
	path := "/events"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result ListEventsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListEvents response: %w", err)
	}
	return &result, nil
}

// RotateOrgWebhookSecret replaces the signing secret of a webhook endpoint and
// returns the new secret. The previous secret stops being used immediately.
func (c *Client) RotateOrgWebhookSecret(ctx context.Context, orgID, webhookID string) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, orgWebhooksPath(orgID)+"/"+webhookID+"/rotate-secret", nil, orgID)
	if err != nil {
		return "", err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return "", err
	}
	var result struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("foundrydb: decode RotateOrgWebhookSecret response: %w", err)
	}
	return result.Secret, nil
}

// EnableOrgWebhook re-enables a webhook endpoint that was disabled manually or
// auto-disabled after persistent delivery failures, clearing its failure streak.
func (c *Client) EnableOrgWebhook(ctx context.Context, orgID, webhookID string) error {
	resp, err := c.do(ctx, http.MethodPost, orgWebhooksPath(orgID)+"/"+webhookID+"/enable", nil, orgID)
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}
