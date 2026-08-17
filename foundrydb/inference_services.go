package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Managed inference services: an open-weight LLM served by vLLM, exposing an
// OpenAI-compatible endpoint on the service's own hostname. This is the service
// management plane (create, list, get, delete an inference service); it is
// distinct from the inference proxy management plane in inference.go.
//
// There are two SKUs, selected by InferenceSKU (or inferred from PlanName):
//
//   - "serverless" multiplexes the service onto a platform-owned shared GPU
//     pool. It takes no plan and rents no card, is limited to curated catalog
//     models a pool is already serving, and is billed per token (and per image
//     for the diffusion models) against the published rate card, with the
//     organization's monthly free token allowance consumed first.
//   - "dedicated" rents a whole-card GPU server for the tenant. It takes a GPU
//     plan, serves curated or Hugging Face models, supports LoRA adapters and
//     keep-warm, and is billed per GPU-hour for as long as the card is
//     allocated rather than per token.
//
// Either way the customer calls EndpointBaseURL with an fdb-inf key. On that
// per-service hostname the model field is
// foundrydb_managed/<served_model_name>; the unprefixed served model name is
// also accepted there as a convenience for apps that hardcode it.

// InferenceSKU selects how an inference service is placed.
type InferenceSKU = string

// Recognized values for InferenceServiceRequest.InferenceSKU.
const (
	// InferenceSKUDedicated rents a whole-card GPU server for the tenant and
	// bills per GPU-hour. It requires a GPU PlanName.
	InferenceSKUDedicated InferenceSKU = "dedicated"
	// InferenceSKUServerless binds the service to a platform-owned shared pool
	// and bills per token. It takes no PlanName.
	InferenceSKUServerless InferenceSKU = "serverless"
)

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
	// Quantization is the format the weights are served at for a Hugging Face
	// model ("" native, or "fp8"), shrinking the footprint so a larger model
	// fits a smaller card. A curated model owns its quantization from the
	// catalog and a request that sets this on one is refused.
	Quantization string `json:"quantization,omitempty"`
	// KVCacheDtype is the vLLM KV-cache quantization ("fp8"). Read-only: it is
	// catalog-owned and never accepted from a create request, which resolves it
	// from the catalog entry (curated) or clears it (Hugging Face).
	KVCacheDtype string `json:"kv_cache_dtype,omitempty"`
	// LicenseAccepted records that the license was accepted. Required before
	// serving a conditional-commercial curated model or any Hugging Face model.
	LicenseAccepted bool `json:"license_accepted,omitempty"`
	// EnableFineTunedServing starts vLLM with LoRA adapter serving enabled so
	// promoted adapters hot-load with no restart. Off by default, and a
	// dedicated-only option: serverless refuses it.
	EnableFineTunedServing bool `json:"enable_fine_tuned_serving,omitempty"`
	// MaxLoRAs and MaxLoRARank bound the concurrently-loaded adapters and their
	// rank. Zero uses the platform defaults. Meaningful only alongside
	// EnableFineTunedServing.
	MaxLoRAs    int `json:"max_loras,omitempty"`
	MaxLoRARank int `json:"max_lora_rank,omitempty"`
	// KeepWarmMinutes auto-stops the service after this many minutes with no
	// inference activity, ending the GPU-hour meter until it is started again
	// (the weights stay on the data disk, so the restart is warm). Zero, the
	// default, never auto-stops; any other value must be between 5 and 10080.
	// Dedicated-only: serverless has no customer GPU to park, so it must be 0.
	KeepWarmMinutes int `json:"keep_warm_minutes,omitempty"`
}

// InferenceService is a managed inference service: an open-weight LLM served by
// vLLM, on a whole-card GPU server (InferenceSKU "dedicated") or on a
// platform-owned shared pool (InferenceSKU "serverless"). The InferenceConfig
// carries the resolved model configuration; its write-only HFToken is never
// returned.
type InferenceService struct {
	ID              string           `json:"id"`
	UserID          string           `json:"user_id"`
	OrganizationID  string           `json:"organization_id,omitempty"`
	Name            string           `json:"name"`
	ServiceKind     string           `json:"service_kind"`
	Status          string           `json:"status"`
	Zone            string           `json:"zone"`
	InferenceSKU    string           `json:"inference_sku,omitempty"`
	PlanName        string           `json:"plan_name"`
	StorageSizeGB   *int             `json:"storage_size_gb,omitempty"`
	StorageTier     string           `json:"storage_tier,omitempty"`
	NodeCount       int              `json:"node_count"`
	InferenceConfig *InferenceConfig `json:"inference_config,omitempty"`
	TLSEnabled      bool             `json:"tls_enabled"`
	ErrorMessage    string           `json:"error_message,omitempty"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
	// EndpointHostname is the service's own edge endpoint host, once
	// provisioned. Empty until the endpoint is minted.
	EndpointHostname string `json:"endpoint_hostname,omitempty"`
	// EndpointBaseURL is the complete OpenAI-compatible base URL to point an SDK
	// at, so no client has to assemble a scheme, a host and a /v1 suffix of its
	// own. It is https://<endpoint_hostname>/v1 once the hostname is minted, and
	// is always a platform address: it is never the upstream the platform
	// forwards to (the shared pool's load balancer, or the GPU serving endpoint),
	// which is internal and not customer-reachable. Call it with an fdb-inf key
	// and the model foundrydb_managed/<served_model_name>.
	EndpointBaseURL string `json:"endpoint_base_url,omitempty"`
	// ProvisioningMessage is the newest live provisioning heartbeat while a
	// deploy is in flight (weight download progress, server start, the readiness
	// wait). Empty once the service is Running or has terminally failed, so it is
	// worth surfacing only while polling a create. Returned by
	// GetInferenceService only.
	ProvisioningMessage string `json:"provisioning_message,omitempty"`
}

// InferenceServiceRequest is the body for CreateInferenceService. Omit
// PlanName (or set InferenceSKU to "serverless") to bind to the platform
// shared pool. A GPU PlanName creates a dedicated whole-card GPU service.
type InferenceServiceRequest struct {
	Name string `json:"name"`
	// InferenceSKU is "dedicated" or "serverless". Empty is inferred from
	// PlanName: a GPU plan is dedicated, no plan is serverless. Combining
	// "serverless" with a GPU plan, or "dedicated" with no plan, is refused.
	InferenceSKU   InferenceSKU     `json:"inference_sku,omitempty"`
	PlanName       string           `json:"plan_name,omitempty"`
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

// CreateInferenceService provisions an inference service and returns its
// initial state. The service is created in the Pending status; poll
// GetInferenceService until it reaches Running and EndpointBaseURL is set.
//
// A GPU PlanName creates a dedicated whole-card service, billed per GPU-hour.
// Omitting PlanName (or setting InferenceSKU to "serverless") binds the service
// to the platform shared pool, billed per token. Serverless additionally
// requires a curated catalog model that a pool is already serving (see
// ListServerlessInferenceModels): an unserved model is refused with 503, and a
// fleet whose pools are all at their binding ceiling with 409.
//
// A conditional curated model, and every Hugging Face model, requires
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

// CreateServerlessInferenceService provisions a serverless inference service on
// the platform shared pool for one curated catalog model, which is the whole of
// what a serverless create takes: there is no plan, no zone, and no serving
// knobs, because the card is the platform's and its serving shape is fixed.
//
// modelID must be a curated catalog id a pool is serving right now; take it from
// ListServerlessInferenceModels rather than guessing, since an unserved model is
// refused. Set licenseAccepted for an accept-gated model. orgID is optional and
// assigns the service to an organization the caller belongs to.
//
// It is a convenience over CreateInferenceService: use that directly to reach
// the dedicated SKU or any other field.
func (c *Client) CreateServerlessInferenceService(ctx context.Context, name, modelID, orgID string, licenseAccepted bool) (*InferenceService, error) {
	return c.CreateInferenceService(ctx, InferenceServiceRequest{
		Name:           name,
		InferenceSKU:   InferenceSKUServerless,
		OrganizationID: orgID,
		Inference: &InferenceConfig{
			ModelID:         modelID,
			ModelSource:     InferenceModelSourceCurated,
			LicenseAccepted: licenseAccepted,
		},
	})
}

// InferenceModelRateUnit says what a published model rate charges per.
type InferenceModelRateUnit = string

// Recognized values for InferenceModelRate.RateUnit.
const (
	// InferenceModelRateUnitTokens prices per token, carried in
	// PromptMicrocentsPer1K and CompletionMicrocentsPer1K.
	InferenceModelRateUnitTokens InferenceModelRateUnit = "tokens"
	// InferenceModelRateUnitImage prices per generated image, carried in
	// ImageMicrocentsPerUnit. The two token figures are zero on such a rate.
	InferenceModelRateUnitImage InferenceModelRateUnit = "image"
)

// InferenceModelRate is one curated model's published price as it stands right
// now, the rate a serverless call on that model is metered at.
type InferenceModelRate struct {
	// ModelID is the curated catalog id, the same id a create request carries in
	// InferenceConfig.ModelID.
	ModelID string `json:"model_id"`
	// RateUnit is "tokens" or "image". An absent value reads as tokens.
	RateUnit InferenceModelRateUnit `json:"rate_unit"`
	// PromptMicrocentsPer1K and CompletionMicrocentsPer1K price the tokens sent
	// to and generated by the model, in microcents per one thousand tokens.
	// Divide by 100,000 for the currency amount per one million tokens. Both are
	// zero on an image-priced rate.
	PromptMicrocentsPer1K     int64 `json:"prompt_microcents_per_1k"`
	CompletionMicrocentsPer1K int64 `json:"completion_microcents_per_1k"`
	// ImageMicrocentsPerUnit prices one generated image. Divide by 100,000,000
	// for the currency amount per image. Absent on a token-priced rate.
	ImageMicrocentsPerUnit int64 `json:"image_microcents_per_unit,omitempty"`
	// EffectiveFrom is when this rate took effect, so a quote can date itself and
	// a cached listing can tell one rate from its successor.
	EffectiveFrom string `json:"effective_from"`
}

type listInferenceModelRatesResponse struct {
	Models []InferenceModelRate `json:"models"`
}

// ListInferenceModelRates returns the price in force right now for every curated
// model that has one, so a create flow can quote what a serverless service will
// cost before anyone commits. It is the same resolution the metering path uses,
// so the quoted price and the billed price cannot diverge.
//
// A model with no rate is omitted rather than reported at zero: zero would read
// as "free", when the truth is that its price is not set yet. An empty slice
// means nothing is priced yet, never an error. The listing is a property of the
// platform, not of the caller's organization.
func (c *Client) ListInferenceModelRates(ctx context.Context) ([]InferenceModelRate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/inference-services/model-rates", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listInferenceModelRatesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListInferenceModelRates response: %w", err)
	}
	return result.Models, nil
}

// ServerlessModelCapability names the surface a serverless model answers on.
type ServerlessModelCapability = string

// Recognized values for ServerlessInferenceModel.Capability.
const (
	// ServerlessCapabilityChat answers /v1/chat/completions and /v1/completions.
	ServerlessCapabilityChat ServerlessModelCapability = "chat"
	// ServerlessCapabilityEmbeddings answers /v1/embeddings.
	ServerlessCapabilityEmbeddings ServerlessModelCapability = "embeddings"
	// ServerlessCapabilityRerank answers /v1/rerank.
	ServerlessCapabilityRerank ServerlessModelCapability = "rerank"
	// ServerlessCapabilityImage answers /v1/images/generations.
	ServerlessCapabilityImage ServerlessModelCapability = "image"
)

// ServerlessInferenceModel is one curated model a shared pool can answer for
// right now, and so one a serverless create can bind to. It describes the model,
// never the pool: pool ids, node counts, and serving URLs are not customer
// surface.
type ServerlessInferenceModel struct {
	// ModelID is the catalog id a create request carries in
	// InferenceConfig.ModelID.
	ModelID string `json:"model_id"`
	// DisplayName is the human-facing catalog name.
	DisplayName string `json:"display_name"`
	// Capability is the surface the model answers on (chat, embeddings, rerank,
	// image), so a picker can group and label its options.
	Capability ServerlessModelCapability `json:"capability"`
	// Serving is always true on a listed model: a model with no serving pool is
	// omitted rather than listed as unavailable. The field is explicit so no
	// client has to infer availability from the listing's mere existence.
	Serving bool `json:"serving"`
	// Deprecated marks a model whose weights are end of life upstream. It is
	// still listed and still bindable, because a pool serves it and the customers
	// already on it must keep working; treat it as retiring and do not make it a
	// default choice.
	Deprecated bool `json:"deprecated"`
}

type listServerlessInferenceModelsResponse struct {
	Models []ServerlessInferenceModel `json:"models"`
}

// ListServerlessInferenceModels returns the curated models a serverless create
// can bind to right now: those a platform pool is already serving. It is the
// question to ask before CreateServerlessInferenceService, which refuses any
// other model.
//
// An empty slice is the honest "serverless has nothing to offer yet" answer
// rather than an error. The dedicated SKU is not constrained by this listing: it
// rents its own card and can serve any curated or Hugging Face model that fits.
func (c *Client) ListServerlessInferenceModels(ctx context.Context) ([]ServerlessInferenceModel, error) {
	resp, err := c.do(ctx, http.MethodGet, "/inference-services/serverless-models", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listServerlessInferenceModelsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListServerlessInferenceModels response: %w", err)
	}
	return result.Models, nil
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

// InferenceModelSwitchRequest is the body for SwitchInferenceModel. The target
// is named by curated catalog id only: a Hugging Face source is not a switch
// target, and no other property of the service (plan, zone, name, keys) can be
// changed through it.
type InferenceModelSwitchRequest struct {
	// ModelID is the curated catalog id to switch to. It must differ from the
	// model the service serves today and must fit the VRAM of the plan the
	// service already runs on.
	ModelID string `json:"model_id"`
	// LicenseAccepted accepts the target model's license. Required to be true
	// when the target is a license-gated curated model (for example Llama), the
	// same acceptance a create of that model demands. Ungated targets ignore it.
	LicenseAccepted bool `json:"license_accepted,omitempty"`
}

// SwitchInferenceModel changes which curated model an existing inference
// service serves, in place. The service's model volume is replaced by a clone
// of the target model's pre-baked volume template when the platform holds one
// for the service's zone (minutes, no weight download), or by a fresh volume
// taking the ordinary download path otherwise; the GPU server, GPU plan,
// endpoint hostname, TLS certificate, firewall rules, inference keys, and
// billing identity are unchanged, and the old volume is deleted only once the
// new model is in place.
//
// The service must be Running or Stopped and single-node, with no other
// transition in flight and no active LoRA adapter bound to the current base
// model (demote it first). Returns the service in the SwitchingModel status;
// poll GetInferenceService until it returns to the state it came from (Running,
// or Stopped for a service switched while stopped, whose next start serves the
// new model).
func (c *Client) SwitchInferenceModel(ctx context.Context, id string, req InferenceModelSwitchRequest) (*InferenceService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/inference-services/"+id+"/switch-model", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var svc InferenceService
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("foundrydb: decode SwitchInferenceModel response: %w", err)
	}
	return &svc, nil
}

// InferenceFitLimitingFactor names the term of the fit equation that broke the
// plan's memory budget.
type InferenceFitLimitingFactor = string

// Recognized values for InferenceFitCheckResult.LimitingFactor.
const (
	// InferenceFitLimitedByWeights means the weights alone exceed the budget, so
	// no context length makes the configuration fit.
	InferenceFitLimitedByWeights InferenceFitLimitingFactor = "weights"
	// InferenceFitLimitedByKVCache means the weights fit but the requested
	// context length does not.
	InferenceFitLimitedByKVCache InferenceFitLimitingFactor = "kv_cache"
	// InferenceFitLimitedByNothing is reported when the configuration fits.
	InferenceFitLimitedByNothing InferenceFitLimitingFactor = "fits"
)

// InferenceFitSuggestionKind is the shape of a proposed fix for a configuration
// that does not fit.
type InferenceFitSuggestionKind = string

// Recognized values for InferenceFitSuggestion.Kind.
const (
	// InferenceFitSuggestionReduceContext serves a shorter context on the same
	// plan; MaxModelLen carries the context length that fits.
	InferenceFitSuggestionReduceContext InferenceFitSuggestionKind = "reduce_context"
	// InferenceFitSuggestionFP8KVCache halves the KV cache instead of shortening
	// the context.
	InferenceFitSuggestionFP8KVCache InferenceFitSuggestionKind = "fp8_kv_cache"
	// InferenceFitSuggestionLargerPlan moves to a bigger GPU plan; PlanName
	// carries the smallest plan whose budget holds the configuration.
	InferenceFitSuggestionLargerPlan InferenceFitSuggestionKind = "larger_plan"
)

// InferenceFitCheckRequest is the body for CheckInferenceFit: a model,
// optionally some serving knobs, and the GPU plan to test it against. Every
// optional field defaults exactly as a create would default it, so leaving them
// zero asks about the configuration a plain create produces.
type InferenceFitCheckRequest struct {
	// ModelSource is "curated" or "huggingface".
	ModelSource InferenceModelSource `json:"model_source"`
	// ModelID is the curated catalog id, or the Hugging Face repo id (org/name)
	// whose config is fetched to size the model.
	ModelID string `json:"model_id"`
	// PlanName is the GPU plan alias to test the model against.
	PlanName string `json:"plan_name"`
	// MaxModelLen is the context length to size the KV cache at. Zero uses the
	// catalog default (curated) or the model-derived maximum (Hugging Face).
	MaxModelLen int `json:"max_model_len,omitempty"`
	// Quantization is the format the weights are served at (for example "fp8",
	// "awq"). Empty uses the checkpoint's own format.
	Quantization string `json:"quantization,omitempty"`
	// KVCacheDType is "auto" (the model dtype) or "fp8", which halves the cache.
	// Empty means auto.
	KVCacheDType string `json:"kv_cache_dtype,omitempty"`
	// GPUMemoryUtilization is the fraction of the plan's VRAM the budget is drawn
	// from, between 0.10 and 0.99. Zero uses the platform default (0.90).
	GPUMemoryUtilization float64 `json:"gpu_memory_utilization,omitempty"`
}

// InferenceFitSuggestion is one concrete way to make a refused configuration
// fit. Suggestions are only offered when they would actually work, so a
// weights-limited refusal never proposes trimming the context.
type InferenceFitSuggestion struct {
	// Kind is "reduce_context", "fp8_kv_cache", or "larger_plan".
	Kind InferenceFitSuggestionKind `json:"kind"`
	// Detail states the fix in caller language and is safe to show verbatim.
	Detail string `json:"detail"`
	// PlanName is the plan to move to. Set on "larger_plan" only.
	PlanName string `json:"plan_name,omitempty"`
	// MaxModelLen is the context length that would fit. Set on "reduce_context"
	// only.
	MaxModelLen int `json:"max_model_len,omitempty"`
}

// InferenceFitCheckResult is the verdict of the VRAM fit preflight, the memory
// breakdown it was reached from, and the closest fixes when it is a refusal.
// All sizes are gibibytes of VRAM.
type InferenceFitCheckResult struct {
	// Fits reports whether weights, KV cache, and overhead together stay within
	// BudgetGB.
	Fits bool `json:"fits"`
	// WeightsGB is the model weights at the resolved dtype and quantization.
	WeightsGB float64 `json:"weights_gb"`
	// KVCacheGB is the KV cache at the resolved context length and cache dtype.
	KVCacheGB float64 `json:"kv_cache_gb"`
	// OverheadGB is the fixed serving allowance (CUDA context, activations, the
	// vLLM runtime). It does not vary with context length.
	OverheadGB float64 `json:"overhead_gb"`
	// BudgetGB is PlanVRAMGB times the effective memory utilization.
	BudgetGB float64 `json:"budget_gb"`
	// PlanVRAMGB is the plan's total VRAM, before the utilization budget.
	PlanVRAMGB int `json:"plan_vram_gb"`
	// MaxContextThatFits is the largest MaxModelLen this plan would serve this
	// model at. Zero when the weights alone exceed the budget.
	MaxContextThatFits int `json:"max_context_that_fits"`
	// LimitingFactor is "weights", "kv_cache", or "fits".
	LimitingFactor InferenceFitLimitingFactor `json:"limiting_factor"`
	// Suggestions are the closest fixes, most relevant first. Empty when the
	// configuration already fits.
	Suggestions []InferenceFitSuggestion `json:"suggestions"`
}

// CheckInferenceFit answers whether a model, at a context length, runs on a GPU
// plan, without provisioning anything: nothing is created, nothing is billed,
// and no GPU is touched.
//
// The fit model is weights + kv_cache(max_model_len) + serving_overhead within
// the memory-utilization budget of the plan's VRAM. Weights follow from the
// parameter count and the served dtype (a curated FP8 checkpoint counts half of
// its BF16 equivalent), the KV cache grows linearly with the served context and
// halves under an fp8 cache, and the overhead is a fixed allowance for the CUDA
// context, activations, and the vLLM runtime. CreateInferenceService and
// SwitchInferenceModel enforce the same equation, so a false Fits here is the
// refusal those calls would return, and Suggestions names the closest fix.
//
// A configuration that does not fit is still a successful call: the question was
// answered. An error is returned for an unknown model or plan (400), or when a
// Hugging Face model's metadata could not be fetched so its size is unknown
// (502); a curated model is sized from the catalog and never hits the latter.
func (c *Client) CheckInferenceFit(ctx context.Context, req InferenceFitCheckRequest) (*InferenceFitCheckResult, error) {
	resp, err := c.do(ctx, http.MethodPost, "/inference-services/fit-check", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result InferenceFitCheckResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CheckInferenceFit response: %w", err)
	}
	return &result, nil
}

// InferenceServiceUsageTotals are the usage counters rolled up across every
// bucket in the requested window.
type InferenceServiceUsageTotals struct {
	Calls          int64 `json:"calls"`
	Errors         int64 `json:"errors"`
	InputTokens    int64 `json:"input_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	TotalTokens    int64 `json:"total_tokens"`
	CostMicrocents int64 `json:"cost_microcents"`
	// Images is how many images the calls generated. It stays zero for a text
	// model, which produces none; an image model meters images rather than
	// output tokens, so it is the only usage figure that moves there.
	Images       int64 `json:"images"`
	AvgLatencyMs int   `json:"avg_latency_ms"`
	// P95LatencyMs is the 95th percentile latency across the window, the tail the
	// slowest callers actually wait for. It is computed over the metered calls
	// rather than folded up from the series, because a percentile is not
	// summable. Zero when the window metered no calls.
	P95LatencyMs int `json:"p95_latency_ms"`
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
	// Images is how many images the calls in this bucket generated; zero for a
	// text model.
	Images       int64 `json:"images"`
	AvgLatencyMs int   `json:"avg_latency_ms"`
	// P95LatencyMs is the 95th percentile latency within this bucket.
	P95LatencyMs int `json:"p95_latency_ms"`
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
	// MonthToDate is the calendar-month rollup, which is what a bill is settled
	// on and which the charted window (24 hours by default) cannot answer. It is
	// unaffected by the requested window.
	MonthToDate *InferenceServiceUsageMonthToDate `json:"month_to_date,omitempty"`
}

// InferenceServiceUsageMonthToDate is one service's calendar-month-to-date
// rollup, independent of the window the caller asked for. Both charges sit on
// it so a client does not issue a second request per range change: Tokens is
// the per-token charge a serverless service accrues, GpuHour the per-GPU-hour
// charge a dedicated one accrues.
type InferenceServiceUsageMonthToDate struct {
	// From is the accounting window start actually used: the first instant of
	// the current UTC month, or the service's creation time when it is younger
	// than the month, so a two-day-old service never claims a full month.
	From string `json:"from"`
	// Tokens is the metered per-token usage over that window, the charge for a
	// serverless service.
	Tokens InferenceServiceUsageTotals `json:"tokens"`
	// GpuHour is the GPU-hour spend over that window, the charge for a dedicated
	// service. Nil when billing has recorded no hour for it this month.
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
// time-bucketed series with rolled-up totals, plus the month-to-date rollup. The
// optional since is a Go duration (for example "1h", "24h") or an RFC3339 start
// time; empty defaults to 24 hours and it is capped at 30 days. The effective
// window never starts before the service was created.
//
// Which figure is the charge depends on the SKU: MonthToDate.Tokens for a
// serverless service (billed per token) and MonthToDate.GpuHour for a dedicated
// one (billed per allocated GPU-hour). The other is a usage signal, not a bill.
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
	Status    InferenceAdapterStatus `json:"status"`
	CreatedAt string                 `json:"created_at"`
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

// DemoteInferenceAdapter stops serving the active LoRA fine-tuned adapter
// version without promoting a replacement: the registry row moves to superseded
// and the adapter is hot-unloaded from the running vLLM, so the served name stops
// answering and its adapter slot is freed. It is the inverse of
// PromoteInferenceAdapter and the only exit from active that does not require
// another version (an in-place model switch is refused while an adapter is
// active, and an active version cannot be deleted). Callers still addressing
// foundrydb_managed/<served_model_name> receive errors afterwards; the service
// keeps serving its base model and the version stays promotable. Requires
// manage-level authority; the request has no body. Returns the adapter after its
// transition to superseded.
func (c *Client) DemoteInferenceAdapter(ctx context.Context, serviceID, adapterID string) (*InferenceModelAdapter, error) {
	resp, err := c.do(ctx, http.MethodPost, "/inference-services/"+serviceID+"/adapters/"+adapterID+"/demote", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result promoteInferenceAdapterResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode DemoteInferenceAdapter response: %w", err)
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
