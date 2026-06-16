package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Inference proxy management plane: organizations bring their own provider
// API keys, mint dedicated data-plane keys for their applications, set
// org-wide policy (EU-only routing, monthly cost circuit breaker), and read
// aggregated usage. The data plane itself is OpenAI-compatible and lives at
// /inference/v1/* authenticated with fdb-inf keys, outside this SDK.

// InferenceProviderConfig is the API view of one configured provider for an
// organization. The provider API key is never returned; HasAPIKey only
// indicates its presence.
type InferenceProviderConfig struct {
	ID         string  `json:"id"`
	Provider   string  `json:"provider"`
	BaseURL    *string `json:"base_url,omitempty"`
	EUEndpoint bool    `json:"eu_endpoint"`
	Enabled    bool    `json:"enabled"`
	HasAPIKey  bool    `json:"has_api_key"`
	EUResident bool    `json:"eu_resident"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// UpsertInferenceProviderRequest creates or replaces an organization's
// provider config. APIKey is required on first configuration; on update an
// empty APIKey keeps the stored one. Provider is one of openai, anthropic,
// mistral, azure_openai; azure_openai requires BaseURL (the Azure resource
// endpoint).
type UpsertInferenceProviderRequest struct {
	Provider   string  `json:"provider"`
	APIKey     string  `json:"api_key"`
	BaseURL    *string `json:"base_url,omitempty"`
	EUEndpoint bool    `json:"eu_endpoint"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

// InferenceKey is the API view of a data-plane key. The secret is never
// returned after creation; KeyPrefix identifies the key in customer code.
type InferenceKey struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	KeyPrefix         string  `json:"key_prefix"`
	MonthlyTokenLimit int64   `json:"monthly_token_limit"`
	RateLimitRPM      int     `json:"rate_limit_rpm"`
	Status            string  `json:"status"`
	TokensUsedCycle   int64   `json:"tokens_used_cycle"`
	CycleMonth        string  `json:"cycle_month"`
	CreatedAt         string  `json:"created_at"`
	RevokedAt         *string `json:"revoked_at,omitempty"`
}

// CreateInferenceKeyRequest mints a new data-plane key. MonthlyTokenLimit is
// required and must be positive: there is no unlimited key.
type CreateInferenceKeyRequest struct {
	Name              string `json:"name"`
	MonthlyTokenLimit int64  `json:"monthly_token_limit"`
	RateLimitRPM      *int   `json:"rate_limit_rpm,omitempty"`
}

// CreateInferenceKeyResult carries the one-time secret alongside the key.
// The secret is shown exactly once and cannot be retrieved again.
type CreateInferenceKeyResult struct {
	Key    InferenceKey `json:"key"`
	Secret string       `json:"secret"`
}

// OrgInferenceSettings holds org-wide proxy policy: EU-only routing and the
// monthly cost circuit breaker.
type OrgInferenceSettings struct {
	OrganizationID        string  `json:"organization_id"`
	EUOnly                bool    `json:"eu_only"`
	MonthlyCostLimitCents int64   `json:"monthly_cost_limit_cents"`
	CircuitOpen           bool    `json:"circuit_open"`
	CircuitOpenedAt       *string `json:"circuit_opened_at,omitempty"`
	UpdatedAt             string  `json:"updated_at"`
}

// UpdateOrgInferenceSettingsRequest updates org-wide proxy policy.
// MonthlyCostLimitCents is required when configuring the settings for the
// first time; ResetCircuit closes an open cost circuit.
type UpdateOrgInferenceSettingsRequest struct {
	EUOnly                *bool  `json:"eu_only,omitempty"`
	MonthlyCostLimitCents *int64 `json:"monthly_cost_limit_cents,omitempty"`
	ResetCircuit          bool   `json:"reset_circuit,omitempty"`
}

// InferenceUsageRow is one aggregated usage row. GroupKey is the model name
// or the key id depending on the requested grouping.
type InferenceUsageRow struct {
	GroupKey       string `json:"group_key"`
	Provider       string `json:"provider"`
	Calls          int64  `json:"calls"`
	InputTokens    int64  `json:"input_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	TotalTokens    int64  `json:"total_tokens"`
	CostMicrocents int64  `json:"cost_microcents"`
}

// InferenceUsageSummary wraps aggregated inference usage for an organization.
type InferenceUsageSummary struct {
	From    string              `json:"from"`
	To      string              `json:"to"`
	GroupBy string              `json:"group_by"`
	Rows    []InferenceUsageRow `json:"rows"`
}

// InferenceUsageOptions filters GetInferenceUsage. From and To are RFC 3339
// timestamps; GroupBy is "model" or "key". Empty fields fall back to the
// API defaults (current month, grouped by model).
type InferenceUsageOptions struct {
	From    string
	To      string
	GroupBy string
}

type listInferenceProvidersResponse struct {
	Providers []InferenceProviderConfig `json:"providers"`
}

type listInferenceKeysResponse struct {
	Keys []InferenceKey `json:"keys"`
}

func orgInferencePath(orgID string) string {
	return "/organizations/" + orgID + "/inference"
}

// ListInferenceProviders returns the organization's configured AI providers.
func (c *Client) ListInferenceProviders(ctx context.Context, orgID string) ([]InferenceProviderConfig, error) {
	resp, err := c.do(ctx, http.MethodGet, orgInferencePath(orgID)+"/providers", nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listInferenceProvidersResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListInferenceProviders response: %w", err)
	}
	return result.Providers, nil
}

// UpsertInferenceProvider creates or replaces the organization's config for
// one provider.
func (c *Client) UpsertInferenceProvider(ctx context.Context, orgID string, req UpsertInferenceProviderRequest) (*InferenceProviderConfig, error) {
	resp, err := c.do(ctx, http.MethodPut, orgInferencePath(orgID)+"/providers", req, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var cfg InferenceProviderConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpsertInferenceProvider response: %w", err)
	}
	return &cfg, nil
}

// DeleteInferenceProvider removes the organization's config for one provider.
// Subsequent proxy calls routed to that provider fail until it is configured
// again; there is no fallback to any platform key.
func (c *Client) DeleteInferenceProvider(ctx context.Context, orgID, provider string) error {
	resp, err := c.do(ctx, http.MethodDelete, orgInferencePath(orgID)+"/providers/"+url.PathEscape(provider), nil, orgID)
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// ListInferenceKeys returns the organization's data-plane keys (prefixes and
// usage counters only, never secrets).
func (c *Client) ListInferenceKeys(ctx context.Context, orgID string) ([]InferenceKey, error) {
	resp, err := c.do(ctx, http.MethodGet, orgInferencePath(orgID)+"/keys", nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listInferenceKeysResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListInferenceKeys response: %w", err)
	}
	return result.Keys, nil
}

// CreateInferenceKey mints a new data-plane key. The returned Secret is shown
// exactly once; store it immediately, it cannot be retrieved again.
func (c *Client) CreateInferenceKey(ctx context.Context, orgID string, req CreateInferenceKeyRequest) (*CreateInferenceKeyResult, error) {
	resp, err := c.do(ctx, http.MethodPost, orgInferencePath(orgID)+"/keys", req, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result CreateInferenceKeyResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateInferenceKey response: %w", err)
	}
	return &result, nil
}

// RevokeInferenceKey revokes a data-plane key. The key row is kept so past
// usage events stay attributable; revocation is immediate and irreversible.
func (c *Client) RevokeInferenceKey(ctx context.Context, orgID, keyID string) error {
	resp, err := c.do(ctx, http.MethodDelete, orgInferencePath(orgID)+"/keys/"+keyID, nil, orgID)
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// GetInferenceSettings returns the organization's proxy policy settings.
// Returns nil, nil when the settings have not been configured yet (404).
func (c *Client) GetInferenceSettings(ctx context.Context, orgID string) (*OrgInferenceSettings, error) {
	resp, err := c.do(ctx, http.MethodGet, orgInferencePath(orgID)+"/settings", nil, orgID)
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
	var settings OrgInferenceSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetInferenceSettings response: %w", err)
	}
	return &settings, nil
}

// UpdateInferenceSettings updates the organization's proxy policy settings,
// creating them when MonthlyCostLimitCents is provided for the first time.
func (c *Client) UpdateInferenceSettings(ctx context.Context, orgID string, req UpdateOrgInferenceSettingsRequest) (*OrgInferenceSettings, error) {
	resp, err := c.do(ctx, http.MethodPut, orgInferencePath(orgID)+"/settings", req, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var settings OrgInferenceSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateInferenceSettings response: %w", err)
	}
	return &settings, nil
}

// GetInferenceUsage returns aggregated inference usage for the organization.
func (c *Client) GetInferenceUsage(ctx context.Context, orgID string, opts InferenceUsageOptions) (*InferenceUsageSummary, error) {
	q := url.Values{}
	if opts.From != "" {
		q.Set("from", opts.From)
	}
	if opts.To != "" {
		q.Set("to", opts.To)
	}
	if opts.GroupBy != "" {
		q.Set("group_by", opts.GroupBy)
	}
	path := orgInferencePath(orgID) + "/usage"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var summary InferenceUsageSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetInferenceUsage response: %w", err)
	}
	return &summary, nil
}
