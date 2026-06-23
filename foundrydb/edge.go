package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
	// EdgeWAFModeBlock inspects, logs, and rejects matching requests with a 403
	// so the malicious request never reaches the origin.
	EdgeWAFModeBlock EdgeWAFMode = "block"
)

// EdgeWAFRuleAction selects what a matching custom WAF rule does: block denies a
// matching request with a 403 (only enforced in block waf_mode; in detect mode
// it still only logs), log only records a match.
type EdgeWAFRuleAction string

const (
	EdgeWAFRuleActionBlock EdgeWAFRuleAction = "block"
	EdgeWAFRuleActionLog   EdgeWAFRuleAction = "log"
)

// EdgeRateLimitKey selects what a rate-limit bucket is keyed on.
type EdgeRateLimitKey string

const (
	EdgeRateLimitKeyIP     EdgeRateLimitKey = "ip"
	EdgeRateLimitKeyAPIKey EdgeRateLimitKey = "api_key"
)

// EdgeRateLimitBackend selects where the token-bucket counter lives. It is
// platform-set by the controller (never by the customer) and is echoed on the
// settings response.
type EdgeRateLimitBackend string

const (
	EdgeRateLimitBackendInProcess EdgeRateLimitBackend = "in_process"
	EdgeRateLimitBackendValkey    EdgeRateLimitBackend = "valkey"
)

// EdgeOriginLBPolicy selects how the edge distributes traffic across the
// combined upstream set (the primary auto origin plus the pool's additional
// origins).
type EdgeOriginLBPolicy string

const (
	EdgeOriginLBPolicyRoundRobin EdgeOriginLBPolicy = "round_robin"
	EdgeOriginLBPolicyWeighted   EdgeOriginLBPolicy = "weighted"
	EdgeOriginLBPolicyLeastConn  EdgeOriginLBPolicy = "least_conn"
	EdgeOriginLBPolicyFirst      EdgeOriginLBPolicy = "first"
)

// EdgeCacheRule caches responses under one path prefix for a fixed TTL.
type EdgeCacheRule struct {
	PathPrefix string `json:"path_prefix"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// EdgeRateLimit is a token bucket enforced per PoP at the edge. The
// RequestsPerSecond, Burst and Key fields are customer-tunable; Backend,
// BackendAddress and NodeCount are platform-set by the controller and only
// echoed back on a response.
type EdgeRateLimit struct {
	RequestsPerSecond int              `json:"requests_per_second"`
	Burst             int              `json:"burst"`
	Key               EdgeRateLimitKey `json:"key"`
	// Backend is the counter location. Empty is treated as in-process. Platform-set.
	Backend EdgeRateLimitBackend `json:"backend,omitempty"`
	// BackendAddress is the Valkey host:port when Backend is valkey; empty otherwise.
	// Platform-set.
	BackendAddress string `json:"backend_address,omitempty"`
	// NodeCount is the number of serving nodes the in-process limit is spread
	// across. Platform-set; empty/0 or 1 means the full limit per node.
	NodeCount int `json:"node_count,omitempty"`
}

// EdgeWAFRuleHeaderMatch matches a named request header's value against a regex.
type EdgeWAFRuleHeaderMatch struct {
	Name         string `json:"name"`
	ValuePattern string `json:"value_pattern"`
}

// EdgeWAFRule is a safe, structured per-app WAF rule the edge compiles into a
// coraza SecRule. The customer supplies only opaque metadata (Name/Description)
// and a small set of match patterns, never any raw SecRule directive text. All
// match fields are optional; at least one is required. When more than one is set
// the fields are ANDed.
type EdgeWAFRule struct {
	Name         string                  `json:"name,omitempty"`
	Description  string                  `json:"description,omitempty"`
	URIPattern   string                  `json:"uri_pattern,omitempty"`
	Method       string                  `json:"method,omitempty"`
	Header       *EdgeWAFRuleHeaderMatch `json:"header,omitempty"`
	SourceIPCIDR string                  `json:"source_ip_cidr,omitempty"`
	Action       EdgeWAFRuleAction       `json:"action"`
}

// EdgeRedirectRule redirects a request whose path exactly matches FromPath to
// ToURL with an HTTP redirect status (one of 301, 302, 307, 308; 0 means the
// default 302). It short-circuits at the edge before WAF, cache, or origin.
type EdgeRedirectRule struct {
	FromPath   string `json:"from_path"`
	ToURL      string `json:"to_url"`
	StatusCode int    `json:"status_code,omitempty"`
}

// EdgeRuleActionType is the closed enum of actions an edge rule may take. Each
// value maps to a fixed edge handler (never raw directive text). Terminal
// actions (redirect, block, origin_override) short-circuit the rule chain (first
// match wins); non-terminal actions (set_header, rewrite, continue) fall through
// to later rules and the rest of the fixed handler chain.
type EdgeRuleActionType string

const (
	EdgeRuleActionRedirect       EdgeRuleActionType = "redirect"
	EdgeRuleActionSetHeader      EdgeRuleActionType = "set_header"
	EdgeRuleActionRewrite        EdgeRuleActionType = "rewrite"
	EdgeRuleActionBlock          EdgeRuleActionType = "block"
	EdgeRuleActionOriginOverride EdgeRuleActionType = "origin_override"
	EdgeRuleActionContinue       EdgeRuleActionType = "continue"
)

// EdgeRuleHeaderMatch matches a named request header. Exactly one of Value
// (exact) or Regex (RE2) is used; Value takes precedence when both are set.
type EdgeRuleHeaderMatch struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Regex string `json:"regex,omitempty"`
}

// EdgeRuleMatch is the ANDed set of conditions an edge rule matches on. Every set
// condition must hold; an empty match matches every request.
type EdgeRuleMatch struct {
	PathPrefix string               `json:"path_prefix,omitempty"`
	PathRegex  string               `json:"path_regex,omitempty"`
	Methods    []string             `json:"methods,omitempty"`
	Header     *EdgeRuleHeaderMatch `json:"header,omitempty"`
}

// EdgeRuleAction is the closed-enum action a matched edge rule takes. Only the
// fields relevant to Type are used.
type EdgeRuleAction struct {
	Type                  EdgeRuleActionType `json:"type"`
	RedirectTo            string             `json:"redirect_to,omitempty"`
	RedirectStatus        int                `json:"redirect_status,omitempty"`
	SetRequestHeaders     map[string]string  `json:"set_request_headers,omitempty"`
	RemoveRequestHeaders  []string           `json:"remove_request_headers,omitempty"`
	SetResponseHeaders    map[string]string  `json:"set_response_headers,omitempty"`
	RemoveResponseHeaders []string           `json:"remove_response_headers,omitempty"`
	Rewrite               string             `json:"rewrite,omitempty"`
	BlockStatus           int                `json:"block_status,omitempty"`
	OriginOverride        *EdgeOrigin        `json:"origin_override,omitempty"`
}

// EdgeRule is one entry in the additive, ordered, composable edge rules engine:
// a match plus a closed-enum action. Rules are evaluated in ascending priority
// order (ties broken by declared index) and compose with the existing fixed edge
// features at a single documented precedence point (after the platform
// short-circuits and the fixed redirects / CORS / method filter / auth, and
// before WAF, rate limit, cache, and the origin).
type EdgeRule struct {
	Name     string         `json:"name,omitempty"`
	Priority int            `json:"priority,omitempty"`
	Match    EdgeRuleMatch  `json:"match"`
	Action   EdgeRuleAction `json:"action"`
}

// EdgeHeaderRules manipulates HTTP headers at the edge. RequestSet/RequestRemove
// apply to the request forwarded to the origin; ResponseSet/ResponseRemove apply
// to the response returned to the client.
type EdgeHeaderRules struct {
	RequestSet     map[string]string `json:"request_set,omitempty"`
	RequestRemove  []string          `json:"request_remove,omitempty"`
	ResponseSet    map[string]string `json:"response_set,omitempty"`
	ResponseRemove []string          `json:"response_remove,omitempty"`
}

// EdgeCORS is the per-app cross-origin resource sharing policy the edge
// enforces. AllowedOrigins is either the single wildcard "*" (only when
// AllowCredentials is false) or a list of concrete http(s) origins.
type EdgeCORS struct {
	AllowedOrigins   []string `json:"allowed_origins,omitempty"`
	AllowedMethods   []string `json:"allowed_methods,omitempty"`
	AllowedHeaders   []string `json:"allowed_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials,omitempty"`
	MaxAgeSeconds    int      `json:"max_age_seconds,omitempty"`
}

// EdgeMaintenance puts an app behind a maintenance page at the edge. When
// Enabled, every client except those whose connection IP is inside a BypassIP
// CIDR gets the maintenance response (StatusCode, default 503, with Body).
type EdgeMaintenance struct {
	Enabled    bool     `json:"enabled"`
	StatusCode int      `json:"status_code,omitempty"`
	Body       string   `json:"body,omitempty"`
	BypassIPs  []string `json:"bypass_ips,omitempty"`
}

// EdgeCompression enables gzip response compression at the edge for one app.
// ExtraContentTypes adds further content-types beyond the runtime defaults.
type EdgeCompression struct {
	Enabled           bool     `json:"enabled"`
	ExtraContentTypes []string `json:"extra_content_types,omitempty"`
}

// EdgeHSTS enables an HTTP Strict-Transport-Security response header at the edge
// for one app. Preload requires IncludeSubdomains and a max-age of at least one
// year.
type EdgeHSTS struct {
	Enabled           bool `json:"enabled"`
	MaxAgeSeconds     int  `json:"max_age_seconds,omitempty"`
	IncludeSubdomains bool `json:"include_subdomains,omitempty"`
	Preload           bool `json:"preload,omitempty"`
}

// EdgeRequestID injects a per-request correlation id at the edge on both the
// request forwarded to the origin and the response returned to the client.
// HeaderName empty defaults to X-Request-ID.
type EdgeRequestID struct {
	Enabled    bool   `json:"enabled"`
	HeaderName string `json:"header_name,omitempty"`
}

// EdgeCanary routes a sticky subset of an app's traffic into a canary (B) arm at
// the edge. A request is routed into the canary arm when it carries the cookie
// MatchCookie or the header MatchHeader (exactly one is set) with the value
// MatchValue; a matched request gets the variant header injected toward the
// origin. VariantHeaderName empty defaults to X-Variant, VariantHeaderValue
// empty defaults to canary.
type EdgeCanary struct {
	Enabled            bool   `json:"enabled"`
	MatchCookie        string `json:"match_cookie,omitempty"`
	MatchHeader        string `json:"match_header,omitempty"`
	MatchValue         string `json:"match_value,omitempty"`
	VariantHeaderName  string `json:"variant_header_name,omitempty"`
	VariantHeaderValue string `json:"variant_header_value,omitempty"`
}

// EdgeOriginHealthCheckActive configures active (out-of-band) origin health
// probing. The edge issues a probe to Path every IntervalSeconds, treating a
// probe that exceeds TimeoutSeconds or returns a status not matching
// ExpectStatus as a failure.
type EdgeOriginHealthCheckActive struct {
	Enabled         bool   `json:"enabled"`
	Path            string `json:"path,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	ExpectStatus    int    `json:"expect_status,omitempty"`
}

// EdgeOriginHealthCheckPassive configures passive (in-band) origin health
// detection: an upstream that returns MaxFails responses whose status is in
// UnhealthyStatus within a rolling FailDurationSeconds window is taken out of
// rotation for that duration.
type EdgeOriginHealthCheckPassive struct {
	MaxFails            int   `json:"max_fails,omitempty"`
	FailDurationSeconds int   `json:"fail_duration_seconds,omitempty"`
	UnhealthyStatus     []int `json:"unhealthy_status,omitempty"`
}

// EdgeOriginHealthCheck is the per-app origin health-check policy the edge
// enforces on the upstream proxy. Either or both of Active and Passive may be set.
type EdgeOriginHealthCheck struct {
	Active  *EdgeOriginHealthCheckActive  `json:"active,omitempty"`
	Passive *EdgeOriginHealthCheckPassive `json:"passive,omitempty"`
}

// EdgeOrigin is one upstream the edge proxies an app's traffic to. A
// customer-configured additional origin carries a Host (hostname or IP), an
// optional SNI (defaults to the dial host), a Weight for weighted load
// balancing, and a Backup flag. FloatingIP is set only on the platform-derived
// primary origin and is read-only.
type EdgeOrigin struct {
	FloatingIP string `json:"floating_ip,omitempty"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port"`
	SNI        string `json:"sni,omitempty"`
	Weight     int    `json:"weight,omitempty"`
	Backup     bool   `json:"backup,omitempty"`
}

// EdgeOriginPool is the per-app set of additional origins beyond the primary
// auto origin, with the load-balancing policy and failover knobs.
type EdgeOriginPool struct {
	AdditionalOrigins  []EdgeOrigin       `json:"additional_origins,omitempty"`
	LBPolicy           EdgeOriginLBPolicy `json:"lb_policy,omitempty"`
	TryDurationSeconds int                `json:"try_duration_seconds,omitempty"`
	Retries            int                `json:"retries,omitempty"`
	RetryStatuses      []int              `json:"retry_statuses,omitempty"`
}

// EdgeBasicAuthAccountRequest is one inbound Basic Auth account on the settings
// request. Password is the PLAINTEXT password the controller bcrypt-hashes and
// discards; it is write-only and never echoed. An empty Password for an existing
// username keeps that account's stored hash.
type EdgeBasicAuthAccountRequest struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// EdgeBasicAuthRequest is the inbound Basic Auth setting on the settings
// request. It carries plaintext passwords that the controller hashes and
// discards; the stored document only ever carries the resulting bcrypt hashes.
type EdgeBasicAuthRequest struct {
	Enabled  bool                          `json:"enabled"`
	Accounts []EdgeBasicAuthAccountRequest `json:"accounts,omitempty"`
}

// EdgeSettingsRequest is the customer-tunable subset of the edge config, written
// via PUT /app-services/{id}/edge/settings. Domains and origin are
// platform-derived and not settable here. Each list/pointer field replaces the
// stored value wholesale; an empty or nil value clears the corresponding setting.
type EdgeSettingsRequest struct {
	CacheRules          []EdgeCacheRule        `json:"cache_rules,omitempty"`
	RateLimit           *EdgeRateLimit         `json:"rate_limit,omitempty"`
	WAFMode             *EdgeWAFMode           `json:"waf_mode,omitempty"`
	CustomWAFRules      []EdgeWAFRule          `json:"custom_waf_rules,omitempty"`
	IPAllowList         []string               `json:"ip_allow_list,omitempty"`
	IPDenyList          []string               `json:"ip_deny_list,omitempty"`
	Redirects           []EdgeRedirectRule     `json:"redirects,omitempty"`
	HeaderRules         *EdgeHeaderRules       `json:"header_rules,omitempty"`
	CORS                *EdgeCORS              `json:"cors,omitempty"`
	Maintenance         *EdgeMaintenance       `json:"maintenance,omitempty"`
	Compression         *EdgeCompression       `json:"compression,omitempty"`
	MaxRequestBodyBytes int64                  `json:"max_request_body_bytes,omitempty"`
	AllowedMethods      []string               `json:"allowed_methods,omitempty"`
	BasicAuth           *EdgeBasicAuthRequest  `json:"basic_auth,omitempty"`
	BlockedPaths        []string               `json:"blocked_paths,omitempty"`
	HSTS                *EdgeHSTS              `json:"hsts,omitempty"`
	RequestID           *EdgeRequestID         `json:"request_id,omitempty"`
	Canary              *EdgeCanary            `json:"canary,omitempty"`
	HealthCheck         *EdgeOriginHealthCheck `json:"health_check,omitempty"`
	OriginPool          *EdgeOriginPool        `json:"origin_pool,omitempty"`
	// CanaryRolloutEnabled opts the app into staged per-node/per-PoP config
	// rollouts: a new config version is dispatched to a canary subset (one node, or
	// one PoP) first and held for a manual promote (with auto-abort on a canary 5xx
	// spike) instead of being dispatched fleet-wide immediately. False (the
	// default) keeps the immediate fleet-wide dispatch behavior.
	CanaryRolloutEnabled bool `json:"canary_rollout_enabled,omitempty"`
	// Rules is the additive, ordered, composable rules engine list. It replaces
	// the stored list wholesale; an empty list (or omitted) clears all rules.
	Rules []EdgeRule `json:"rules,omitempty"`
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

// EdgeSettings echoes the customer-tunable edge settings after an update (and on
// GET .../edge/settings). Basic Auth password hashes are never echoed; only the
// BasicAuthEnabled flag and BasicAuthUsernames are returned.
type EdgeSettings struct {
	CacheRules          []EdgeCacheRule        `json:"cache_rules,omitempty"`
	RateLimit           *EdgeRateLimit         `json:"rate_limit,omitempty"`
	WAFMode             EdgeWAFMode            `json:"waf_mode"`
	CustomWAFRules      []EdgeWAFRule          `json:"custom_waf_rules,omitempty"`
	IPAllowList         []string               `json:"ip_allow_list,omitempty"`
	IPDenyList          []string               `json:"ip_deny_list,omitempty"`
	Redirects           []EdgeRedirectRule     `json:"redirects,omitempty"`
	HeaderRules         *EdgeHeaderRules       `json:"header_rules,omitempty"`
	CORS                *EdgeCORS              `json:"cors,omitempty"`
	Maintenance         *EdgeMaintenance       `json:"maintenance,omitempty"`
	Compression         *EdgeCompression       `json:"compression,omitempty"`
	MaxRequestBodyBytes int64                  `json:"max_request_body_bytes,omitempty"`
	AllowedMethods      []string               `json:"allowed_methods,omitempty"`
	BasicAuthEnabled    bool                   `json:"basic_auth_enabled"`
	BasicAuthUsernames  []string               `json:"basic_auth_usernames,omitempty"`
	BlockedPaths        []string               `json:"blocked_paths,omitempty"`
	HSTS                *EdgeHSTS              `json:"hsts,omitempty"`
	RequestID           *EdgeRequestID         `json:"request_id,omitempty"`
	Canary              *EdgeCanary            `json:"canary,omitempty"`
	HealthCheck         *EdgeOriginHealthCheck `json:"health_check,omitempty"`
	OriginPool          *EdgeOriginPool        `json:"origin_pool,omitempty"`
	// CanaryRolloutEnabled reports whether the app opts into staged
	// per-node/per-PoP config rollouts instead of immediate fleet-wide dispatch.
	CanaryRolloutEnabled bool `json:"canary_rollout_enabled"`
	// Rules is the additive, ordered, composable rules engine list; empty means no
	// rules.
	Rules         []EdgeRule `json:"rules,omitempty"`
	ConfigVersion int64      `json:"config_version"`
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

// GetAppEdgeSettings returns the customer-tunable edge settings currently stored
// for an app service, plus the desired-state config version the fleet converges
// on. Basic Auth password hashes are never returned (only the enabled flag and
// the usernames).
func (c *Client) GetAppEdgeSettings(ctx context.Context, appServiceID string) (*EdgeSettings, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/edge/settings", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var settings EdgeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppEdgeSettings response: %w", err)
	}
	return &settings, nil
}

// EdgeCachePurgeRequest is the body of POST /app-services/{id}/edge/cache/purge.
// Exactly one form is requested: All drops every cached entry for the app on the
// fleet, or Paths invalidates the cached entries under each listed absolute path.
type EdgeCachePurgeRequest struct {
	All   bool     `json:"all,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// EdgeCachePurgeResponse reports the rolling purge plan the request started. The
// purge flushes nodes one at a time in the background, so the endpoint returns
// the plan rather than the completed result.
type EdgeCachePurgeResponse struct {
	PlannedNodes int      `json:"planned_nodes"`
	NodeIDs      []string `json:"node_ids,omitempty"`
	Rolling      bool     `json:"rolling"`
}

// PurgeAppEdgeCache flushes the app's edge cache across its serving PoP nodes,
// either entirely (req.All) or for the listed absolute paths (req.Paths); set
// exactly one. The purge rolls across nodes one at a time in the background, so
// the response reports the plan (planned node count and ids) rather than the
// completed result.
func (c *Client) PurgeAppEdgeCache(ctx context.Context, appServiceID string, req EdgeCachePurgeRequest) (*EdgeCachePurgeResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/edge/cache/purge", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out EdgeCachePurgeResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PurgeAppEdgeCache response: %w", err)
	}
	return &out, nil
}

// EdgeMetricsTopPath is one (path, count) entry of a top-paths or
// suspicious-paths list in the edge analytics summary.
type EdgeMetricsTopPath struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// EdgeStatusClassCounts breaks a request total down by HTTP status class.
type EdgeStatusClassCounts struct {
	C2xx int64 `json:"2xx"`
	C3xx int64 `json:"3xx"`
	C4xx int64 `json:"4xx"`
	C5xx int64 `json:"5xx"`
}

// EdgeCacheCounts is the cache hit/miss summary with the derived hit ratio.
type EdgeCacheCounts struct {
	Hit      int64   `json:"hit"`
	Miss     int64   `json:"miss"`
	HitRatio float64 `json:"hit_ratio"`
}

// EdgeLatencyPercentiles holds the latency percentiles (milliseconds) estimated
// from the request latency histogram.
type EdgeLatencyPercentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

// EdgeAnalyticsThreat is the per-scope security/threat summary: the WAF
// detection total plus the observed top paths matching credential-scanner
// shapes.
type EdgeAnalyticsThreat struct {
	WAFDetectionsTotal int64                `json:"waf_detections_total"`
	SuspiciousPaths    []EdgeMetricsTopPath `json:"suspicious_paths"`
}

// EdgeAnalyticsSummary is the folded edge analytics for one scope (the app
// total or one PoP) over the window. Zone is empty for the app-wide total.
type EdgeAnalyticsSummary struct {
	Zone               string                 `json:"zone,omitempty"`
	RequestsTotal      int64                  `json:"requests_total"`
	ByStatusClass      EdgeStatusClassCounts  `json:"by_status_class"`
	ErrorRatePct       float64                `json:"error_rate_pct"`
	Cache              EdgeCacheCounts        `json:"cache"`
	RateLimitedTotal   int64                  `json:"rate_limited_total"`
	WAFDetectionsTotal int64                  `json:"waf_detections_total"`
	WAFByRule          map[string]int64       `json:"waf_by_rule,omitempty"`
	LatencyMs          EdgeLatencyPercentiles `json:"latency_ms"`
	TopPaths           []EdgeMetricsTopPath   `json:"top_paths"`
	Threat             EdgeAnalyticsThreat    `json:"threat"`
}

// EdgeAnalytics is the GET /app-services/{id}/edge/analytics response: an
// account-scoped, server-aggregated edge analytics summary for one app over a
// time window, folded across the app's PoPs with a per-PoP breakdown.
type EdgeAnalytics struct {
	WindowMinutes int                    `json:"window_minutes"`
	Total         EdgeAnalyticsSummary   `json:"total"`
	PoPs          []EdgeAnalyticsSummary `json:"pops"`
}

// GetAppEdgeAnalytics returns the account-scoped edge analytics summary for an
// app over windowMinutes (pass 0 to use the server default of 60 minutes). The
// summary covers the request status breakdown, error rate, cache hit ratio,
// latency percentiles, rate-limited and WAF detection counts, top paths, and a
// suspicious-path threat summary, folded across the app's PoPs with a per-PoP
// breakdown.
func (c *Client) GetAppEdgeAnalytics(ctx context.Context, appServiceID string, windowMinutes int) (*EdgeAnalytics, error) {
	path := "/app-services/" + appServiceID + "/edge/analytics"
	if windowMinutes > 0 {
		path += "?window_minutes=" + strconv.Itoa(windowMinutes)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var analytics EdgeAnalytics
	if err := json.Unmarshal(data, &analytics); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppEdgeAnalytics response: %w", err)
	}
	return &analytics, nil
}

// EdgeIPRedactionMode controls how a log drain transforms the client IP before
// a line leaves the platform.
type EdgeIPRedactionMode string

const (
	EdgeIPFull      EdgeIPRedactionMode = "full"
	EdgeIPTruncated EdgeIPRedactionMode = "truncated"
	EdgeIPHashed    EdgeIPRedactionMode = "hashed"
	EdgeIPOmitted   EdgeIPRedactionMode = "omitted"
)

// EdgeRedactionPolicy is the per-drain privacy policy applied to every access
// log line before export. Authorization and Cookie are always dropped
// regardless of HeaderAllowList.
type EdgeRedactionPolicy struct {
	IPMode           EdgeIPRedactionMode `json:"ip_mode,omitempty"`
	IPHashSalt       string              `json:"ip_hash_salt,omitempty"`
	StripQueryString *bool               `json:"strip_query_string,omitempty"`
	HeaderAllowList  []string            `json:"header_allow_list,omitempty"`
}

// EdgeLogDrain streams an app's per-request edge access logs to a customer
// destination. The destination configuration is write-only and never returned.
type EdgeLogDrain struct {
	ID                    string              `json:"id"`
	AppServiceID          string              `json:"app_service_id"`
	Name                  string              `json:"name"`
	Description           string              `json:"description"`
	DestinationType       string              `json:"destination_type"`
	RedactionPolicy       EdgeRedactionPolicy `json:"redaction_policy"`
	IsEnabled             bool                `json:"is_enabled"`
	ExportIntervalSeconds int                 `json:"export_interval_seconds"`
	LastExportAt          *string             `json:"last_export_at,omitempty"`
	LastExportError       string              `json:"last_export_error,omitempty"`
	ConsecutiveFailures   int                 `json:"consecutive_failures"`
	CreatedAt             string              `json:"created_at"`
	UpdatedAt             string              `json:"updated_at"`
}

// CreateEdgeLogDrainRequest creates an edge access-log drain. Configuration is
// destination-specific (s3: endpoint/region/bucket/prefix/access_key_id/
// secret_access_key; webhook: url/auth_header_name/auth_header_value).
type CreateEdgeLogDrainRequest struct {
	Name                  string               `json:"name"`
	Description           string               `json:"description,omitempty"`
	DestinationType       string               `json:"destination_type"`
	Configuration         map[string]any       `json:"configuration"`
	RedactionPolicy       *EdgeRedactionPolicy `json:"redaction_policy,omitempty"`
	IsEnabled             *bool                `json:"is_enabled,omitempty"`
	ExportIntervalSeconds int                  `json:"export_interval_seconds,omitempty"`
}

// UpdateEdgeLogDrainRequest is a partial update; omitted fields keep their value.
type UpdateEdgeLogDrainRequest struct {
	Name                  *string              `json:"name,omitempty"`
	Description           *string              `json:"description,omitempty"`
	DestinationType       *string              `json:"destination_type,omitempty"`
	Configuration         map[string]any       `json:"configuration,omitempty"`
	RedactionPolicy       *EdgeRedactionPolicy `json:"redaction_policy,omitempty"`
	IsEnabled             *bool                `json:"is_enabled,omitempty"`
	ExportIntervalSeconds *int                 `json:"export_interval_seconds,omitempty"`
}

type listEdgeLogDrainsResponse struct {
	Drains []EdgeLogDrain `json:"drains"`
}

// ListEdgeLogDrains lists the app's edge access-log drains.
func (c *Client) ListEdgeLogDrains(ctx context.Context, appServiceID string) ([]EdgeLogDrain, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/edge/log-drains", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out listEdgeLogDrainsResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListEdgeLogDrains response: %w", err)
	}
	return out.Drains, nil
}

// CreateEdgeLogDrain creates a new edge access-log drain for the app.
func (c *Client) CreateEdgeLogDrain(ctx context.Context, appServiceID string, req CreateEdgeLogDrainRequest) (*EdgeLogDrain, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/edge/log-drains", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var drain EdgeLogDrain
	if err := json.Unmarshal(data, &drain); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateEdgeLogDrain response: %w", err)
	}
	return &drain, nil
}

// GetEdgeLogDrain returns one edge access-log drain.
func (c *Client) GetEdgeLogDrain(ctx context.Context, appServiceID, drainID string) (*EdgeLogDrain, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/edge/log-drains/"+drainID, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var drain EdgeLogDrain
	if err := json.Unmarshal(data, &drain); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetEdgeLogDrain response: %w", err)
	}
	return &drain, nil
}

// UpdateEdgeLogDrain partially updates an edge access-log drain.
func (c *Client) UpdateEdgeLogDrain(ctx context.Context, appServiceID, drainID string, req UpdateEdgeLogDrainRequest) (*EdgeLogDrain, error) {
	resp, err := c.do(ctx, http.MethodPut, "/app-services/"+appServiceID+"/edge/log-drains/"+drainID, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var drain EdgeLogDrain
	if err := json.Unmarshal(data, &drain); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateEdgeLogDrain response: %w", err)
	}
	return &drain, nil
}

// DeleteEdgeLogDrain deletes an edge access-log drain, stopping all future
// exports for it.
func (c *Client) DeleteEdgeLogDrain(ctx context.Context, appServiceID, drainID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/app-services/"+appServiceID+"/edge/log-drains/"+drainID, nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// EdgeLogDrainTestResult reports whether a drain's destination is reachable.
type EdgeLogDrainTestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TestEdgeLogDrain verifies connectivity to the drain's destination without
// sending real log data.
func (c *Client) TestEdgeLogDrain(ctx context.Context, appServiceID, drainID string) (*EdgeLogDrainTestResult, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/edge/log-drains/"+drainID+"/test", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result EdgeLogDrainTestResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode TestEdgeLogDrain response: %w", err)
	}
	return &result, nil
}

// EdgeConfigVersion is one entry in the append-only edge config version history.
// The live edge configuration is the single source of truth for what is active;
// this history is the immutable audit trail and the source a rollback restores
// from.
type EdgeConfigVersion struct {
	Version    int64  `json:"version"`
	ConfigHash string `json:"config_hash"`
	// Source is what produced this version: "reconcile" (a platform recompute
	// bump), "settings" (a customer settings write), or "rollback" (a restore of
	// a prior version's customer-settable subset).
	Source string `json:"source"`
	// CreatedBy is the user that initiated the change, when attributable
	// (settings or rollback). Nil for reconciler bumps.
	CreatedBy *string `json:"created_by,omitempty"`
	CreatedAt string  `json:"created_at"`
	// Active reports whether this version is the currently active (live) version.
	Active bool `json:"active"`
	// RolledBackFrom, for a rollback version, is the version whose
	// customer-settable subset it restored. Nil for any other source.
	RolledBackFrom *int64 `json:"rolled_back_from,omitempty"`
}

// EdgeConfigVersions is the GET /app-services/{id}/edge/versions response: the
// app's edge config version history (newest first, bounded) and the live active
// version.
type EdgeConfigVersions struct {
	ActiveVersion int64               `json:"active_version"`
	Versions      []EdgeConfigVersion `json:"versions"`
}

// EdgeRollbackRequest names the version to roll back to. Supply exactly one of
// ToVersion (an explicit positive version) or To set to "previous" (the version
// immediately before the active one).
type EdgeRollbackRequest struct {
	ToVersion int64  `json:"to_version,omitempty"`
	To        string `json:"to,omitempty"`
}

// EdgeRollbackResponse reports the new active version a rollback produced. The
// rollback writes a NEW forward version restoring the target's customer-settable
// subset; it never mutates the history.
type EdgeRollbackResponse struct {
	ActiveVersion  int64  `json:"active_version"`
	RolledBackFrom int64  `json:"rolled_back_from"`
	Source         string `json:"source"`
}

// ListAppEdgeConfigVersions returns the append-only version history of an app
// service's edge configuration, newest first, plus the live active version. Use
// it to find a version to roll back to with RollbackAppEdgeConfig.
func (c *Client) ListAppEdgeConfigVersions(ctx context.Context, appServiceID string) (*EdgeConfigVersions, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/edge/versions", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out EdgeConfigVersions
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppEdgeConfigVersions response: %w", err)
	}
	return &out, nil
}

// RollbackAppEdgeConfig rolls an app service's edge configuration back to a prior
// version. Supply exactly one of req.ToVersion or req.To="previous". The rollback
// restores the target version's customer-settable subset onto the live
// configuration as a NEW forward version (keeping the current platform-derived
// domains and origin); it never mutates the history. The edge fleet converges on
// the new version asynchronously (poll GetAppEdgeStatus). The response returns
// the new active version.
func (c *Client) RollbackAppEdgeConfig(ctx context.Context, appServiceID string, req EdgeRollbackRequest) (*EdgeRollbackResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/edge/rollback", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out EdgeRollbackResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RollbackAppEdgeConfig response: %w", err)
	}
	return &out, nil
}

// EdgeRollout is one staged edge config rollout. A rollout stages a new config
// version to a canary subset (one node, or one PoP) first, then either promotes
// it to the rest of the fleet or aborts (the rest is never given the version).
type EdgeRollout struct {
	ID            string `json:"id"`
	TargetVersion int64  `json:"target_version"`
	// Phase is one of "canary" (held on the subset), "promoting" (fanning out to
	// the whole fleet), "promoted" (whole fleet converged), or "aborted" (the rest
	// of the fleet was never given the version).
	Phase string `json:"phase"`
	// CanaryScope is "node" (CanarySelector is a VM UUID) or "pop" (CanarySelector
	// is a zone code).
	CanaryScope    string  `json:"canary_scope"`
	CanarySelector string  `json:"canary_selector,omitempty"`
	StartedAt      string  `json:"started_at"`
	UpdatedAt      string  `json:"updated_at"`
	PromotedAt     *string `json:"promoted_at,omitempty"`
	AbortedAt      *string `json:"aborted_at,omitempty"`
	AbortReason    *string `json:"abort_reason,omitempty"`
}

// EdgeRolloutStatus is the GET /app-services/{id}/edge/rollout response: the
// app's current (or most recent) rollout. Active reports whether the rollout is
// in a non-terminal phase (canary or promoting); Rollout is nil when the app has
// never had a rollout.
type EdgeRolloutStatus struct {
	Active  bool         `json:"active"`
	Rollout *EdgeRollout `json:"rollout,omitempty"`
}

// EdgeRolloutAbortRequest carries an optional operator note recorded as the
// rollout's abort reason. An empty Reason records a default "manual abort" note.
type EdgeRolloutAbortRequest struct {
	Reason string `json:"reason,omitempty"`
}

// GetAppEdgeRollout returns the app service's current staged config rollout (the
// active one, or the most recent terminal one), or Active=false with a nil
// rollout when the app has never had one. Canary rollouts are opened by the
// platform when the app's edge settings enable CanaryRolloutEnabled and a new
// config version is produced.
func (c *Client) GetAppEdgeRollout(ctx context.Context, appServiceID string) (*EdgeRolloutStatus, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/edge/rollout", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out EdgeRolloutStatus
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppEdgeRollout response: %w", err)
	}
	return &out, nil
}

// PromoteAppEdgeRollout promotes a holding canary rollout so the platform fans
// the canary version out to the rest of the fleet. Only an active rollout in the
// canary phase can be promoted.
func (c *Client) PromoteAppEdgeRollout(ctx context.Context, appServiceID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/edge/rollout/promote", nil, "")
	if err != nil {
		return err
	}
	if _, err := checkResponse(resp); err != nil {
		return err
	}
	return nil
}

// AbortAppEdgeRollout aborts an active rollout. The rest of the fleet was never
// given the target version, so it keeps serving the prior version; the canary
// subset can be recovered with RollbackAppEdgeConfig. Reason is an optional
// operator note.
func (c *Client) AbortAppEdgeRollout(ctx context.Context, appServiceID string, req EdgeRolloutAbortRequest) error {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/edge/rollout/abort", req, "")
	if err != nil {
		return err
	}
	if _, err := checkResponse(resp); err != nil {
		return err
	}
	return nil
}
