package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Managed inference services: an open-weight LLM served by vLLM on a dedicated
// GPU server, exposing an OpenAI-compatible endpoint. This is the service
// management plane (create, list, get, delete a GPU inference server); it is
// distinct from the inference proxy management plane in inference.go. Once
// running, a managed inference service is reached through the proxy as the
// provider foundrydb_managed/<served_model_name>.

// InferenceModelSource selects how a model's weights are obtained.
type InferenceModelSource = string

// Recognized values for InferenceConfig.ModelSource.
const (
	// InferenceModelSourceCurated is a blessed catalog model the platform has
	// license-verified.
	InferenceModelSourceCurated InferenceModelSource = "curated"
	// InferenceModelSourceHuggingFace is an on-demand pull by Hugging Face repo
	// id; the customer owns the license.
	InferenceModelSourceHuggingFace InferenceModelSource = "huggingface"
)

// InferenceConfig is the model selection and vLLM serving knobs for an
// inference service. For a curated model the platform resolves the repository,
// served name, and context length from the catalog. For a Hugging Face model,
// ModelID is the org/name repo id and ServedModelName is required.
type InferenceConfig struct {
	// ModelID is the catalog id for a curated model, or the Hugging Face repo id
	// (org/name) for an on-demand pull.
	ModelID string `json:"model_id"`
	// ModelSource is "curated" or "huggingface".
	ModelSource InferenceModelSource `json:"model_source"`
	// ServedModelName is the name the OpenAI-compatible endpoint reports and
	// clients pass as the "model" field. Required for a Hugging Face model.
	ServedModelName string `json:"served_model_name,omitempty"`
	// HFRepo is the Hugging Face repository vLLM loads the weights from. Resolved
	// by the platform for curated models.
	HFRepo string `json:"hf_repo,omitempty"`
	// HFToken authenticates pulls of gated repositories. Write-only: it is
	// accepted on create and never returned by any response. Empty for ungated
	// models.
	HFToken string `json:"hf_token,omitempty"`
	// DType is the vLLM weight dtype ("auto", "bfloat16", "float16"). Empty means
	// "auto".
	DType string `json:"dtype,omitempty"`
	// MaxModelLen caps the served context length. Zero uses the catalog default
	// (curated) or the model-derived maximum (Hugging Face).
	MaxModelLen int `json:"max_model_len,omitempty"`
	// GPUMemoryUtilization is the fraction of VRAM vLLM reserves for the KV
	// cache. Zero uses the platform default (0.90).
	GPUMemoryUtilization float64 `json:"gpu_memory_utilization,omitempty"`
	// TensorParallelSize splits the model across N cards. Zero uses 1; values
	// above 1 require a multi-card GPU plan and must divide the plan's card
	// count.
	TensorParallelSize int `json:"tensor_parallel_size,omitempty"`
	// LicenseAccepted records that the license was accepted. Required before
	// serving a conditional-commercial curated model or any Hugging Face model.
	LicenseAccepted bool `json:"license_accepted,omitempty"`
}

// InferenceService is a managed inference service: an open-weight LLM served by
// vLLM on a dedicated GPU server. The InferenceConfig carries the resolved
// model configuration; its write-only HFToken is never returned.
type InferenceService struct {
	ID              string           `json:"id"`
	UserID          string           `json:"user_id"`
	OrganizationID  string           `json:"organization_id,omitempty"`
	Name            string           `json:"name"`
	ServiceKind     string           `json:"service_kind"`
	Status          string           `json:"status"`
	Zone            string           `json:"zone"`
	PlanName        string           `json:"plan_name"`
	StorageSizeGB   *int             `json:"storage_size_gb,omitempty"`
	StorageTier     string           `json:"storage_tier,omitempty"`
	NodeCount       int              `json:"node_count"`
	InferenceConfig *InferenceConfig `json:"inference_config,omitempty"`
	TLSEnabled      bool             `json:"tls_enabled"`
	ErrorMessage    string           `json:"error_message,omitempty"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
	// EndpointHostname is the service's dedicated edge endpoint host, once
	// provisioned. The OpenAI-compatible base URL is
	// https://<endpoint_hostname>/v1, called with an fdb-inf key and the model
	// foundrydb_managed/<served_model_name>. Empty until the endpoint is minted.
	EndpointHostname string `json:"endpoint_hostname,omitempty"`
}

// InferenceServiceRequest is the body for CreateInferenceService. Phase 1 is
// HEL2 GPU plans, whole-card dedicated. InferenceConfig is required.
type InferenceServiceRequest struct {
	Name           string           `json:"name"`
	PlanName       string           `json:"plan_name"`
	Zone           string           `json:"zone,omitempty"`
	Inference      *InferenceConfig `json:"inference_config"`
	OrganizationID string           `json:"organization_id,omitempty"`
}

type listInferenceServicesResponse struct {
	InferenceServices []InferenceService `json:"inference_services"`
}

// ListInferenceServices returns the inference services visible to the
// authenticated user (the active organization's, or the caller's own).
func (c *Client) ListInferenceServices(ctx context.Context) ([]InferenceService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/inference-services", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listInferenceServicesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListInferenceServices response: %w", err)
	}
	return result.InferenceServices, nil
}

// GetInferenceService returns the inference service with the given UUID.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetInferenceService(ctx context.Context, id string) (*InferenceService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/inference-services/"+id, nil, "")
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
	var svc InferenceService
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetInferenceService response: %w", err)
	}
	return &svc, nil
}

// CreateInferenceService provisions an open-weight LLM on a new dedicated GPU
// server and returns its initial state. The service is created in the Pending
// status; poll GetInferenceService until it reaches Running. A conditional
// curated model, and every Hugging Face model, requires
// req.Inference.LicenseAccepted to be true. The write-only HFToken is accepted
// here and never returned.
func (c *Client) CreateInferenceService(ctx context.Context, req InferenceServiceRequest) (*InferenceService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/inference-services", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var svc InferenceService
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateInferenceService response: %w", err)
	}
	return &svc, nil
}

// DeleteInferenceService initiates deletion of the inference service. The
// platform tears down the vLLM runtime, ingress, certificates, DNS, floating
// IP, and the GPU server. A 404 response is treated as success (idempotent).
func (c *Client) DeleteInferenceService(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/inference-services/"+id, nil, "")
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

// InferenceServiceUsageTotals are the usage counters rolled up across every
// bucket in the requested window.
type InferenceServiceUsageTotals struct {
	Calls          int64   `json:"calls"`
	Errors         int64   `json:"errors"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	TotalTokens    int64   `json:"total_tokens"`
	CostMicrocents int64   `json:"cost_microcents"`
	AvgLatencyMs   int     `json:"avg_latency_ms"`
	// ErrorRate is errors/calls, 0 when there are no calls.
	ErrorRate float64 `json:"error_rate"`
}

// InferenceServiceUsagePoint is one time bucket of usage. Empty buckets are
// omitted from the series.
type InferenceServiceUsagePoint struct {
	BucketStart    string `json:"bucket_start"`
	Calls          int64  `json:"calls"`
	Errors         int64  `json:"errors"`
	InputTokens    int64  `json:"input_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	TotalTokens    int64  `json:"total_tokens"`
	CostMicrocents int64  `json:"cost_microcents"`
	AvgLatencyMs   int    `json:"avg_latency_ms"`
}

// InferenceServiceUsage is a service's metered usage over a window: rolled-up
// totals plus the ordered bucket series. Usage is attributed to the service's
// dedicated endpoint within the owning organization, so two services serving the
// same model never share each other's usage, and the window never starts before
// the service was created.
type InferenceServiceUsage struct {
	ServiceID     string                       `json:"service_id"`
	From          string                       `json:"from"`
	To            string                       `json:"to"`
	BucketSeconds int                          `json:"bucket_seconds"`
	Totals        InferenceServiceUsageTotals  `json:"totals"`
	Series        []InferenceServiceUsagePoint `json:"series"`
	// GpuHour is the real GPU-hour spend for a dedicated inference service,
	// summed from the billing snapshots that charge it. A dedicated endpoint
	// bills per GPU-hour while running, not per token, so this is the actual
	// cost while Totals.CostMicrocents (per token) stays 0 for an in-house
	// model. Nil when billing has not yet recorded an hour for the service.
	GpuHour *InferenceServiceGpuHourCost `json:"gpu_hour,omitempty"`
}

// InferenceServiceGpuHourCost is the accrued GPU-hour spend for a dedicated
// inference service over the usage window, in EUR. BilledHours is the number of
// hourly billing snapshots counted; HourlyRateEUR is the most recent hourly
// rate; CostEUR is the summed spend, approximately HourlyRateEUR * BilledHours.
type InferenceServiceGpuHourCost struct {
	BilledHours   int64   `json:"billed_hours"`
	HourlyRateEUR float64 `json:"hourly_rate_eur"`
	CostEUR       float64 `json:"cost_eur"`
}

// GetInferenceServiceUsage returns the service's metered usage and cost as a
// time-bucketed series with rolled-up totals. The optional since is a Go
// duration (for example "1h", "24h") or an RFC3339 start time; empty defaults to
// 24 hours. The effective window never starts before the service was created.
func (c *Client) GetInferenceServiceUsage(ctx context.Context, id, since string) (*InferenceServiceUsage, error) {
	path := "/inference-services/" + id + "/usage"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
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
	var usage InferenceServiceUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetInferenceServiceUsage response: %w", err)
	}
	return &usage, nil
}

// InferenceGPUStats is a single GPU's hardware telemetry sampled from
// nvidia-smi on the inference node. Memory is in mebibytes and power in watts.
type InferenceGPUStats struct {
	Index       int     `json:"index"`
	UtilPercent float64 `json:"util_percent"`
	MemUsedMB   float64 `json:"mem_used_mb"`
	MemTotalMB  float64 `json:"mem_total_mb"`
	TempC       float64 `json:"temp_c"`
	PowerW      float64 `json:"power_w"`
}

// InferenceServerMetricsSnapshot is one sampled reading of a GPU inference
// node's live serving telemetry: the vLLM OpenAI server's own Prometheus
// metrics plus the node's GPU hardware counters. Token throughput and the
// average-latency fields are interval rates derived on the node from the delta
// between two consecutive scrapes; the first scrape after start reports zero
// for those derived fields.
type InferenceServerMetricsSnapshot struct {
	// CollectedAt is when the agent took this reading (UTC).
	CollectedAt string `json:"collected_at"`
	// ModelName is the served model label vLLM reports on its metrics.
	ModelName string `json:"model_name,omitempty"`
	// ServerReachable is false when the vLLM /metrics endpoint could not be
	// scraped this tick (still starting, crash-looping, or draining). The GPU
	// fields may still be present in that case.
	ServerReachable bool `json:"server_reachable"`

	// Instantaneous vLLM gauges.
	RequestsRunning   float64 `json:"requests_running"`
	RequestsWaiting   float64 `json:"requests_waiting"`
	GPUCacheUsagePerc float64 `json:"gpu_cache_usage_perc"` // 0..1, fraction of KV cache blocks in use

	// Derived token throughput over the interval since the previous scrape.
	GenerationTokensPerSec float64 `json:"generation_tokens_per_sec"`
	PromptTokensPerSec     float64 `json:"prompt_tokens_per_sec"`

	// Derived average latencies over the interval, in milliseconds. Zero when no
	// requests completed in the interval.
	AvgTimeToFirstTokenMs   float64 `json:"avg_ttft_ms"`
	AvgTimePerOutputTokenMs float64 `json:"avg_tpot_ms"`
	AvgE2ELatencyMs         float64 `json:"avg_e2e_latency_ms"`

	// RequestsSuccessTotal is the cumulative successful request count (monotonic;
	// charted as a delta).
	RequestsSuccessTotal float64 `json:"requests_success_total"`

	// GPUs holds one entry per physical GPU visible on the node (nvidia-smi
	// order). Empty when nvidia-smi is unavailable.
	GPUs []InferenceGPUStats `json:"gpus,omitempty"`
}

// InferenceServiceMetrics is the live-metrics payload for one inference
// service: the ordered snapshot series over the requested window plus the most
// recent snapshot for the realtime tiles. It is the live vLLM + GPU telemetry
// the inference node samples, distinct from the metered usage and cost served
// by GetInferenceServiceUsage.
type InferenceServiceMetrics struct {
	ServiceID string                           `json:"service_id"`
	From      string                           `json:"from"`
	To        string                           `json:"to"`
	Snapshots []InferenceServerMetricsSnapshot `json:"snapshots"`
	Latest    *InferenceServerMetricsSnapshot  `json:"latest,omitempty"`
}

// GetInferenceServiceMetrics returns the service's live vLLM + GPU serving
// telemetry as an ordered snapshot series with the most recent snapshot broken
// out as Latest. The optional since is a Go duration (for example "30m", "1h")
// or an RFC3339 start time; empty defaults to 30 minutes and the window is
// capped at 24 hours. Returns nil, nil when the service does not exist (404).
func (c *Client) GetInferenceServiceMetrics(ctx context.Context, id, since string) (*InferenceServiceMetrics, error) {
	path := "/inference-services/" + id + "/metrics"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
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
	var metrics InferenceServiceMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetInferenceServiceMetrics response: %w", err)
	}
	return &metrics, nil
}

// InferenceAdapterStatus is the lifecycle status of a LoRA fine-tuned adapter in
// the serving registry.
type InferenceAdapterStatus = string

// Recognized values for InferenceModelAdapter.Status.
const (
	// InferenceAdapterStatusUploaded means the adapter weights are in Files and
	// the registry row exists, but it is not yet loaded onto a GPU.
	InferenceAdapterStatusUploaded InferenceAdapterStatus = "uploaded"
	// InferenceAdapterStatusActive means the adapter is currently loaded into
	// vLLM and serving. At most one active version per service and served model.
	InferenceAdapterStatusActive InferenceAdapterStatus = "active"
	// InferenceAdapterStatusSuperseded means the adapter was replaced by a newer
	// promoted version; it is kept so a rollback can re-promote it.
	InferenceAdapterStatusSuperseded InferenceAdapterStatus = "superseded"
	// InferenceAdapterStatusArchived means the adapter is retired and no longer
	// promotable.
	InferenceAdapterStatusArchived InferenceAdapterStatus = "archived"
)

// InferenceModelAdapter is one version of a customer LoRA fine-tuned adapter in
// the serving registry. The adapter is trained on the organization's data and
// its weights stored in Files (object storage); promoting it downloads the
// weights onto the base-model GPU, verifies their hash, and hot-loads them into
// vLLM. Once active, the service answers to the adapter as
// foundrydb_managed/<served_model_name> on the OpenAI-compatible endpoint. An
// adapter never leaves its owning organization's boundary.
type InferenceModelAdapter struct {
	ID string `json:"id"`
	// OrganizationID is the owning organization; an adapter is only servable on
	// that organization's GPU.
	OrganizationID string `json:"organization_id"`
	// InferenceServiceID is the service currently serving this adapter. Nil while
	// the row is only uploaded and not yet promoted.
	InferenceServiceID *string `json:"inference_service_id,omitempty"`
	// BaseModelID is the base model the adapter was trained against; promote
	// rejects a mismatch with the service's model.
	BaseModelID string `json:"base_model_id"`
	// ServedModelName is the customer-facing name the adapter answers to in the
	// OpenAI-wire model field (foundrydb_managed/<served_model_name>).
	ServedModelName string `json:"served_model_name"`
	// Version is monotonic per organization and served model name. Rollback
	// re-promotes a prior version.
	Version int `json:"version"`
	// FilesBucket and FilesKeyPrefix locate the adapter artifact in Files.
	FilesBucket    string `json:"files_bucket"`
	FilesKeyPrefix string `json:"files_key_prefix"`
	// AdapterSHA256 is the hash of the adapter weights, verified after download
	// before loading so a tampered or partial artifact never serves.
	AdapterSHA256 string `json:"adapter_sha256"`
	// SizeBytes is the artifact size, for the vLLM adapter slot and VRAM headroom
	// budget.
	SizeBytes int64 `json:"size_bytes"`
	// BaseModelLicense travels with the weights; promote enforces its
	// commercial-use terms. Empty is allowed.
	BaseModelLicense string `json:"base_model_license,omitempty"`
	// Status is the lifecycle state (uploaded, active, superseded, archived).
	Status InferenceAdapterStatus `json:"status"`
	CreatedAt string `json:"created_at"`
	// PromotedAt is set when the adapter last became active. Nil until first
	// promote.
	PromotedAt *string `json:"promoted_at,omitempty"`
	DeletedAt  *string `json:"deleted_at,omitempty"`
}

type listInferenceServiceAdaptersResponse struct {
	Adapters []InferenceModelAdapter `json:"adapters"`
}

type promoteInferenceAdapterResponse struct {
	Adapter *InferenceModelAdapter `json:"adapter"`
}

// ListInferenceServiceAdapters returns the LoRA fine-tuned adapter versions
// relevant to the service, newest first: the versions bound to it (the
// currently active version plus its superseded history) together with the
// organization's uploaded, not-yet-promoted versions trained on this service's
// base model, so a freshly registered adapter can be promoted from here. An
// uploaded version carries status "uploaded" until it is promoted; uploaded
// versions for another base model, organization, or service are not listed.
// Returns an empty slice when nothing is bound or promotable, and nil, nil when
// the service does not exist (404).
func (c *Client) ListInferenceServiceAdapters(ctx context.Context, serviceID string) ([]InferenceModelAdapter, error) {
	resp, err := c.do(ctx, http.MethodGet, "/inference-services/"+serviceID+"/adapters", nil, "")
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
	var result listInferenceServiceAdaptersResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListInferenceServiceAdapters response: %w", err)
	}
	return result.Adapters, nil
}

// PromoteInferenceAdapter promotes a LoRA fine-tuned adapter version onto the
// service's serving GPU: the platform downloads the adapter weights from Files,
// verifies their hash, and hot-loads them into vLLM with no restart. The
// promoted version becomes active and any previously active version is marked
// superseded. Rollback is achieved through this same method by promoting a prior
// (superseded) version. Requires manage-level authority; the request has no
// body. Returns the promoted adapter after its transition to active.
func (c *Client) PromoteInferenceAdapter(ctx context.Context, serviceID, adapterID string) (*InferenceModelAdapter, error) {
	resp, err := c.do(ctx, http.MethodPost, "/inference-services/"+serviceID+"/adapters/"+adapterID+"/promote", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result promoteInferenceAdapterResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PromoteInferenceAdapter response: %w", err)
	}
	return result.Adapter, nil
}

// DeleteInferenceAdapter removes one LoRA fine-tuned adapter version from the
// organization's serving registry. It is the lifecycle counterpart to
// RegisterInferenceAdapter: an uploaded (never-promoted) or superseded
// (rolled-off) version can be removed so the registry does not accumulate stale
// rows. An actively-served version is refused (409): promote a different version
// or delete the inference service first. Organization-scoped; a cross-org,
// unknown, or already-removed adapter id returns not-found (a soft-deleted
// version is invisible to reads, so a repeat delete is a 404).
func (c *Client) DeleteInferenceAdapter(ctx context.Context, adapterID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/inference-services/adapters/"+adapterID, nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// InferenceAdapterRegisterRequest is the body for RegisterInferenceAdapter. The
// producer (the fine-tuning workflow) sends it after uploading the LoRA adapter
// artifact to the organization's Files bucket, to record an uploaded, promotable
// version in the serving registry. The owning organization is resolved from the
// caller's auth, or from OrganizationID when set and the caller is a member of
// it; it is never trusted from the artifact.
type InferenceAdapterRegisterRequest struct {
	// OrganizationID registers the adapter under a specific organization the
	// caller belongs to (a platform admin may target any). Empty uses the
	// caller's active organization.
	OrganizationID string `json:"organization_id,omitempty"`
	// BaseModelID is the base model the adapter was trained against; it must
	// later match the serving service's model id or Hugging Face repo.
	BaseModelID string `json:"base_model_id"`
	// ServedModelName is the customer-facing name the adapter answers to,
	// becoming foundrydb_managed/<served_model_name>. Letters, digits, '.', '_'
	// and '-' only, at most 128 characters.
	ServedModelName string `json:"served_model_name"`
	// Version is monotonic per (organization, served model name) and must be at
	// least 1.
	Version int `json:"version"`
	// FilesBucket is the organization's Files bucket holding the adapter
	// artifact.
	FilesBucket string `json:"files_bucket"`
	// FilesKeyPrefix is the Files key prefix holding adapter_model.safetensors
	// and adapter_config.json.
	FilesKeyPrefix string `json:"files_key_prefix"`
	// AdapterSHA256 is the 64-character lowercase hex sha256 of
	// adapter_model.safetensors, re-verified after download before loading.
	AdapterSHA256 string `json:"adapter_sha256"`
	// SizeBytes is the artifact size in bytes and must not be negative.
	SizeBytes int64 `json:"size_bytes"`
	// BaseModelLicense is the base-model license that travels with the weights.
	// Optional.
	BaseModelLicense string `json:"base_model_license,omitempty"`
}

type registerInferenceAdapterResponse struct {
	Adapter *InferenceModelAdapter `json:"adapter"`
}

// RegisterInferenceAdapter records an uploaded LoRA fine-tuned adapter version in
// the serving registry, making it promotable. Call it after uploading the adapter
// artifact (adapter_model.safetensors and adapter_config.json) to the
// organization's Files bucket; PromoteInferenceAdapter later binds the version to
// a GPU and hot-loads it. The row is org-scoped and unbound (its
// InferenceServiceID is nil) until promote, and it enters the registry with
// status "uploaded". Returns the registered adapter.
func (c *Client) RegisterInferenceAdapter(ctx context.Context, req InferenceAdapterRegisterRequest) (*InferenceModelAdapter, error) {
	resp, err := c.do(ctx, http.MethodPost, "/inference-services/adapters", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result registerInferenceAdapterResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RegisterInferenceAdapter response: %w", err)
	}
	return result.Adapter, nil
}
