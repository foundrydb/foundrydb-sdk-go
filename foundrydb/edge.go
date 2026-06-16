package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// EdgeDomainStatus tracks a customer custom domain through its lifecycle on
// the edge tier.
type EdgeDomainStatus string

const (
	EdgeDomainStatusPendingVerification EdgeDomainStatus = "pending_verification"
	EdgeDomainStatusVerifying           EdgeDomainStatus = "verifying"
	EdgeDomainStatusIssuingCertificate  EdgeDomainStatus = "issuing_certificate"
	EdgeDomainStatusPropagating         EdgeDomainStatus = "propagating"
	EdgeDomainStatusActive              EdgeDomainStatus = "active"
	EdgeDomainStatusFailed              EdgeDomainStatus = "failed"
	EdgeDomainStatusDeleting            EdgeDomainStatus = "deleting"
)

// EdgeDomain is one customer custom domain attached to an app service, served
// through the edge tier.
type EdgeDomain struct {
	ID                    string           `json:"id"`
	ServiceID             string           `json:"service_id"`
	UserID                string           `json:"user_id"`
	Domain                string           `json:"domain"`
	Status                EdgeDomainStatus `json:"status"`
	CertificateID         *string          `json:"certificate_id,omitempty"`
	VerificationCheckedAt *string          `json:"verification_checked_at,omitempty"`
	ErrorMessage          *string          `json:"error_message,omitempty"`
	// CNAMETarget is the platform hostname the customer points their DNS at.
	CNAMETarget string `json:"cname_target,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// CreateEdgeDomainRequest adds a custom domain to an app service via
// POST /app-services/{id}/domains.
type CreateEdgeDomainRequest struct {
	Domain string `json:"domain"`
}

// EdgeWAFMode selects how the edge web application firewall treats a
// matching request for one app.
type EdgeWAFMode string

const (
	EdgeWAFModeOff    EdgeWAFMode = "off"
	EdgeWAFModeDetect EdgeWAFMode = "detect"
)

// EdgeRateLimitKey selects what a rate-limit bucket is keyed on.
type EdgeRateLimitKey string

const (
	EdgeRateLimitKeyIP     EdgeRateLimitKey = "ip"
	EdgeRateLimitKeyAPIKey EdgeRateLimitKey = "api_key"
)

// EdgeCacheRule caches responses under one path prefix for a fixed TTL.
type EdgeCacheRule struct {
	PathPrefix string `json:"path_prefix"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// EdgeRateLimit is a token bucket enforced per PoP at the edge.
type EdgeRateLimit struct {
	RequestsPerSecond int              `json:"requests_per_second"`
	Burst             int              `json:"burst"`
	Key               EdgeRateLimitKey `json:"key"`
}

// EdgeSettingsRequest is the customer-tunable subset of the edge config.
// Domains and origin are platform-derived and not settable here.
type EdgeSettingsRequest struct {
	CacheRules []EdgeCacheRule  `json:"cache_rules,omitempty"`
	RateLimit  *EdgeRateLimit   `json:"rate_limit,omitempty"`
	WAFMode    *EdgeWAFMode     `json:"waf_mode,omitempty"`
}

// EdgeApplicationStatusItem is one PoP's convergence state.
type EdgeApplicationStatusItem struct {
	Zone           string `json:"zone"`
	AppliedVersion int64  `json:"applied_version"`
	Status         string `json:"status"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

// EdgeStatus is the app's edge overview: where it is served from and how far
// the fleet has converged.
type EdgeStatus struct {
	EdgeEnabled bool   `json:"edge_enabled"`
	HomePoP     string `json:"home_pop,omitempty"`
	// CNAMETarget is the platform hostname custom domains point at.
	CNAMETarget   string                      `json:"cname_target,omitempty"`
	ConfigVersion int64                       `json:"config_version"`
	Applications  []EdgeApplicationStatusItem `json:"applications,omitempty"`
}

// EdgeSettings echoes the customer-tunable edge settings after an update.
type EdgeSettings struct {
	CacheRules    []EdgeCacheRule `json:"cache_rules,omitempty"`
	RateLimit     *EdgeRateLimit  `json:"rate_limit,omitempty"`
	WAFMode       EdgeWAFMode     `json:"waf_mode"`
	ConfigVersion int64           `json:"config_version"`
}

// listEdgeDomainsResponse wraps the list-domains response envelope.
type listEdgeDomainsResponse struct {
	Domains []EdgeDomain `json:"domains"`
}

// ListAppDomains returns all custom domains attached to the given app service.
func (c *Client) ListAppDomains(ctx context.Context, appServiceID string) ([]EdgeDomain, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/domains", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listEdgeDomainsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppDomains response: %w", err)
	}
	return result.Domains, nil
}

// CreateAppDomain adds a custom domain to an app service. The domain is
// created in pending_verification status; call VerifyAppDomain to trigger an
// immediate verification pass, or wait for the background worker.
func (c *Client) CreateAppDomain(ctx context.Context, appServiceID string, req CreateEdgeDomainRequest) (*EdgeDomain, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/domains", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var d EdgeDomain
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateAppDomain response: %w", err)
	}
	return &d, nil
}

// VerifyAppDomain requeues a failed or pending domain for an immediate
// verification pass. Returns a 202 Accepted on success (no body decoded).
func (c *Client) VerifyAppDomain(ctx context.Context, appServiceID, domainID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/domains/"+domainID+"/verify", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// DeleteAppDomain removes a custom domain from an app service. A 404 response
// is treated as success (idempotent).
func (c *Client) DeleteAppDomain(ctx context.Context, appServiceID, domainID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/app-services/"+appServiceID+"/domains/"+domainID, nil, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil
	}
	_, err = checkResponse(resp)
	return err
}

// GetAppEdgeStatus returns the edge overview for an app service: whether the
// edge tier is enabled, the home PoP, CNAME target, desired-state version, and
// per-PoP convergence status.
func (c *Client) GetAppEdgeStatus(ctx context.Context, appServiceID string) (*EdgeStatus, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/edge", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var status EdgeStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppEdgeStatus response: %w", err)
	}
	return &status, nil
}

// UpdateAppEdgeSettings replaces the customer-tunable edge settings (cache
// rules, rate limit, WAF mode) for an app service. Domains and origin are
// platform-derived and cannot be set here. Returns the updated settings plus
// the config version the fleet will converge on.
func (c *Client) UpdateAppEdgeSettings(ctx context.Context, appServiceID string, req EdgeSettingsRequest) (*EdgeSettings, error) {
	resp, err := c.do(ctx, http.MethodPut, "/app-services/"+appServiceID+"/edge/settings", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var settings EdgeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateAppEdgeSettings response: %w", err)
	}
	return &settings, nil
}
