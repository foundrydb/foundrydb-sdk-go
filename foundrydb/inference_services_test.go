package foundrydb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rawInferenceServiceJSON is a verbatim sample of the wire format the platform
// returns for a managed inference service. Tests decode THIS (not a struct
// round-trip) so a silently-wrong json tag is caught: round-trip tests would
// re-encode with the same wrong tag and pass.
const rawInferenceServiceJSON = `{
  "id": "11111111-1111-1111-1111-111111111111",
  "user_id": "22222222-2222-2222-2222-222222222222",
  "organization_id": "33333333-3333-3333-3333-333333333333",
  "name": "my-llm",
  "service_kind": "inference",
  "status": "Running",
  "zone": "fi-hel2",
  "inference_sku": "dedicated",
  "plan_name": "gpu-l40s-1",
  "storage_size_gb": 200,
  "storage_tier": "maxiops",
  "node_count": 1,
  "inference_config": {
    "model_id": "llama-3.1-8b-instruct",
    "model_source": "curated",
    "served_model_name": "llama-3.1-8b-instruct",
    "hf_repo": "meta-llama/Llama-3.1-8B-Instruct",
    "dtype": "bfloat16",
    "max_model_len": 8192,
    "gpu_memory_utilization": 0.9,
    "tensor_parallel_size": 1,
    "kv_cache_dtype": "fp8",
    "license_accepted": true,
    "enable_fine_tuned_serving": true,
    "max_loras": 4,
    "max_lora_rank": 16,
    "keep_warm_minutes": 30
  },
  "tls_enabled": true,
  "error_message": null,
  "created_at": "2026-08-01T10:00:00Z",
  "updated_at": "2026-08-01T10:30:00Z",
  "endpoint_hostname": "my-llm-a1b2c3.inf.foundrydb.com",
  "endpoint_base_url": "https://my-llm-a1b2c3.inf.foundrydb.com/v1",
  "provisioning_message": "downloading weights (42%)"
}`

func TestInferenceService_WireFormat(t *testing.T) {
	var s InferenceService
	if err := json.Unmarshal([]byte(rawInferenceServiceJSON), &s); err != nil {
		t.Fatalf("unmarshal inference service: %v", err)
	}

	if s.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("id: got %q (check the `id` json tag)", s.ID)
	}
	if s.OrganizationID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("organization_id: got %q", s.OrganizationID)
	}
	if s.Name != "my-llm" {
		t.Errorf("name: got %q", s.Name)
	}
	if s.ServiceKind != "inference" {
		t.Errorf("service_kind: got %q", s.ServiceKind)
	}
	if s.InferenceSKU != InferenceSKUDedicated {
		t.Errorf("inference_sku: got %q, want %q", s.InferenceSKU, InferenceSKUDedicated)
	}
	if s.PlanName != "gpu-l40s-1" {
		t.Errorf("plan_name: got %q", s.PlanName)
	}
	if s.StorageSizeGB == nil || *s.StorageSizeGB != 200 {
		t.Errorf("storage_size_gb: got %v", s.StorageSizeGB)
	}
	if s.NodeCount != 1 {
		t.Errorf("node_count: got %d", s.NodeCount)
	}
	if s.EndpointHostname != "my-llm-a1b2c3.inf.foundrydb.com" {
		t.Errorf("endpoint_hostname: got %q", s.EndpointHostname)
	}
	if s.EndpointBaseURL != "https://my-llm-a1b2c3.inf.foundrydb.com/v1" {
		t.Errorf("endpoint_base_url: got %q", s.EndpointBaseURL)
	}
	if s.ProvisioningMessage != "downloading weights (42%)" {
		t.Errorf("provisioning_message: got %q", s.ProvisioningMessage)
	}
	if !s.TLSEnabled {
		t.Error("tls_enabled: got false")
	}

	cfg := s.InferenceConfig
	if cfg == nil {
		t.Fatal("inference_config: got nil")
	}
	if cfg.ModelID != "llama-3.1-8b-instruct" {
		t.Errorf("inference_config.model_id: got %q", cfg.ModelID)
	}
	if cfg.ModelSource != InferenceModelSourceCurated {
		t.Errorf("inference_config.model_source: got %q", cfg.ModelSource)
	}
	if cfg.HFRepo != "meta-llama/Llama-3.1-8B-Instruct" {
		t.Errorf("inference_config.hf_repo: got %q", cfg.HFRepo)
	}
	if cfg.MaxModelLen != 8192 {
		t.Errorf("inference_config.max_model_len: got %d", cfg.MaxModelLen)
	}
	if cfg.GPUMemoryUtilization != 0.9 {
		t.Errorf("inference_config.gpu_memory_utilization: got %v", cfg.GPUMemoryUtilization)
	}
	if cfg.KVCacheDtype != "fp8" {
		t.Errorf("inference_config.kv_cache_dtype: got %q", cfg.KVCacheDtype)
	}
	if !cfg.EnableFineTunedServing {
		t.Error("inference_config.enable_fine_tuned_serving: got false")
	}
	if cfg.MaxLoRAs != 4 || cfg.MaxLoRARank != 16 {
		t.Errorf("inference_config lora bounds: got max_loras=%d max_lora_rank=%d", cfg.MaxLoRAs, cfg.MaxLoRARank)
	}
	if cfg.KeepWarmMinutes != 30 {
		t.Errorf("inference_config.keep_warm_minutes: got %d", cfg.KeepWarmMinutes)
	}
}

// TestInferenceConfig_HFTokenIsWriteOnly pins that the write-only token is sent
// on a create but never expected back: it must serialize, and it must be absent
// from a response body that does not carry it.
func TestInferenceConfig_HFTokenIsWriteOnly(t *testing.T) {
	b, err := json.Marshal(InferenceConfig{
		ModelID:     "org/model",
		ModelSource: InferenceModelSourceHuggingFace,
		HFToken:     "hf_secret",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if m["hf_token"] != "hf_secret" {
		t.Errorf("hf_token not sent on the wire: got %v", m["hf_token"])
	}

	var svc InferenceService
	if err := json.Unmarshal([]byte(rawInferenceServiceJSON), &svc); err != nil {
		t.Fatalf("unmarshal service: %v", err)
	}
	if svc.InferenceConfig.HFToken != "" {
		t.Errorf("hf_token decoded from a response that omits it: got %q", svc.InferenceConfig.HFToken)
	}
}

// TestInferenceServiceRequest_WireFormat pins the request body field names the
// platform expects on a create.
func TestInferenceServiceRequest_WireFormat(t *testing.T) {
	req := InferenceServiceRequest{
		Name:         "my-llm",
		InferenceSKU: InferenceSKUDedicated,
		PlanName:     "gpu-l40s-1",
		Zone:         "fi-hel2",
		Inference: &InferenceConfig{
			ModelID:     "llama-3.1-8b-instruct",
			ModelSource: InferenceModelSourceCurated,
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"name", "inference_sku", "plan_name", "zone", "inference_config"} {
		if _, ok := m[key]; !ok {
			t.Errorf("request body missing field %q (json tag regression)", key)
		}
	}
	if m["inference_sku"] != "dedicated" {
		t.Errorf("inference_sku serialized as %v", m["inference_sku"])
	}
}

func TestListInferenceServices_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"inference_services":[` + rawInferenceServiceJSON + `]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	list, err := c.ListInferenceServices(context.Background())
	if err != nil {
		t.Fatalf("ListInferenceServices: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 service, got %d", len(list))
	}
	if list[0].Name != "my-llm" {
		t.Errorf("decoded name: got %q (check the `inference_services` envelope tag)", list[0].Name)
	}
}

// TestGetInferenceService_NotFound confirms 404 maps to nil, nil.
func TestGetInferenceService_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	s, err := c.GetInferenceService(context.Background(), "nope")
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if s != nil {
		t.Errorf("expected nil service on 404, got %+v", s)
	}
}

// TestCreateServerlessInferenceService_RequestShape verifies the convenience
// wrapper sends the serverless SKU with a curated model and no plan: a plan
// would be refused by the platform.
func TestCreateServerlessInferenceService_RequestShape(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["inference_sku"] != "serverless" {
			t.Errorf("inference_sku: got %v", body["inference_sku"])
		}
		if _, ok := body["plan_name"]; ok {
			t.Error("plan_name must be omitted on a serverless create")
		}
		if body["organization_id"] != orgID {
			t.Errorf("organization_id: got %v", body["organization_id"])
		}
		cfg, _ := body["inference_config"].(map[string]any)
		if cfg["model_id"] != "llama-3.1-8b-instruct" {
			t.Errorf("inference_config.model_id: got %v", cfg["model_id"])
		}
		if cfg["model_source"] != "curated" {
			t.Errorf("inference_config.model_source: got %v", cfg["model_source"])
		}
		if cfg["license_accepted"] != true {
			t.Errorf("inference_config.license_accepted: got %v", cfg["license_accepted"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(rawInferenceServiceJSON))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	s, err := c.CreateServerlessInferenceService(context.Background(), "my-llm", "llama-3.1-8b-instruct", orgID, true)
	if err != nil {
		t.Fatalf("CreateServerlessInferenceService: %v", err)
	}
	if s.Name != "my-llm" {
		t.Errorf("decoded name: got %q", s.Name)
	}
}

// TestDeleteInferenceService_NotFoundIsSuccess pins the idempotent delete.
func TestDeleteInferenceService_NotFoundIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services/gone" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.DeleteInferenceService(context.Background(), "gone"); err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
}

func TestListInferenceModelRates_WireFormat(t *testing.T) {
	const raw = `{"models":[
		{
			"model_id": "llama-3.1-8b-instruct",
			"rate_unit": "tokens",
			"prompt_microcents_per_1k": 1500,
			"completion_microcents_per_1k": 3000,
			"effective_from": "2026-07-01T00:00:00Z"
		},
		{
			"model_id": "flux-schnell",
			"rate_unit": "image",
			"prompt_microcents_per_1k": 0,
			"completion_microcents_per_1k": 0,
			"image_microcents_per_unit": 300000,
			"effective_from": "2026-07-01T00:00:00Z"
		}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference-services/model-rates" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	rates, err := c.ListInferenceModelRates(context.Background())
	if err != nil {
		t.Fatalf("ListInferenceModelRates: %v", err)
	}
	if len(rates) != 2 {
		t.Fatalf("expected 2 rates, got %d", len(rates))
	}
	if rates[0].RateUnit != InferenceModelRateUnitTokens {
		t.Errorf("rate_unit: got %q", rates[0].RateUnit)
	}
	if rates[0].PromptMicrocentsPer1K != 1500 || rates[0].CompletionMicrocentsPer1K != 3000 {
		t.Errorf("token rate: got prompt=%d completion=%d", rates[0].PromptMicrocentsPer1K, rates[0].CompletionMicrocentsPer1K)
	}
	if rates[1].RateUnit != InferenceModelRateUnitImage {
		t.Errorf("rate_unit: got %q", rates[1].RateUnit)
	}
	if rates[1].ImageMicrocentsPerUnit != 300000 {
		t.Errorf("image_microcents_per_unit: got %d", rates[1].ImageMicrocentsPerUnit)
	}
	if rates[1].EffectiveFrom != "2026-07-01T00:00:00Z" {
		t.Errorf("effective_from: got %q", rates[1].EffectiveFrom)
	}
}

func TestListServerlessInferenceModels_WireFormat(t *testing.T) {
	const raw = `{"models":[
		{"model_id":"llama-3.1-8b-instruct","display_name":"Llama 3.1 8B Instruct","capability":"chat","serving":true,"deprecated":false},
		{"model_id":"bge-m3","display_name":"BGE M3","capability":"embeddings","serving":true,"deprecated":true}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference-services/serverless-models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	models, err := c.ListServerlessInferenceModels(context.Background())
	if err != nil {
		t.Fatalf("ListServerlessInferenceModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Capability != ServerlessCapabilityChat {
		t.Errorf("capability: got %q", models[0].Capability)
	}
	if !models[0].Serving {
		t.Error("serving: got false on a listed model")
	}
	if models[1].Capability != ServerlessCapabilityEmbeddings {
		t.Errorf("capability: got %q", models[1].Capability)
	}
	if !models[1].Deprecated {
		t.Error("deprecated: got false")
	}
	if models[1].DisplayName != "BGE M3" {
		t.Errorf("display_name: got %q", models[1].DisplayName)
	}
}

func TestSwitchInferenceModel_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services/svc-1/switch-model" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model_id"] != "qwen2.5-14b-instruct" {
			t.Errorf("model_id: got %v", body["model_id"])
		}
		if body["license_accepted"] != true {
			t.Errorf("license_accepted: got %v", body["license_accepted"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rawInferenceServiceJSON))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	s, err := c.SwitchInferenceModel(context.Background(), "svc-1", InferenceModelSwitchRequest{
		ModelID:         "qwen2.5-14b-instruct",
		LicenseAccepted: true,
	})
	if err != nil {
		t.Fatalf("SwitchInferenceModel: %v", err)
	}
	if s.ID == "" {
		t.Error("expected the switching service to decode")
	}
}

func TestCheckInferenceFit_WireFormat(t *testing.T) {
	const raw = `{
		"fits": false,
		"weights_gb": 16.1,
		"kv_cache_gb": 28.4,
		"overhead_gb": 2.0,
		"budget_gb": 43.2,
		"plan_vram_gb": 48,
		"max_context_that_fits": 16384,
		"limiting_factor": "kv_cache",
		"suggestions": [
			{"kind":"reduce_context","detail":"Serve at 16384 tokens of context on this plan.","max_model_len":16384},
			{"kind":"fp8_kv_cache","detail":"Halve the KV cache with an fp8 cache dtype."},
			{"kind":"larger_plan","detail":"Move to gpu-h100-1.","plan_name":"gpu-h100-1"}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services/fit-check" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		for _, key := range []string{"model_source", "model_id", "plan_name", "max_model_len"} {
			if _, ok := body[key]; !ok {
				t.Errorf("request body missing field %q (json tag regression)", key)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	res, err := c.CheckInferenceFit(context.Background(), InferenceFitCheckRequest{
		ModelSource: InferenceModelSourceCurated,
		ModelID:     "llama-3.1-70b-instruct",
		PlanName:    "gpu-l40s-1",
		MaxModelLen: 32768,
	})
	if err != nil {
		t.Fatalf("CheckInferenceFit: %v", err)
	}
	// A configuration that does not fit is still a successful call.
	if res.Fits {
		t.Error("fits: got true")
	}
	if res.WeightsGB != 16.1 || res.KVCacheGB != 28.4 || res.OverheadGB != 2.0 {
		t.Errorf("memory breakdown: got weights=%v kv=%v overhead=%v", res.WeightsGB, res.KVCacheGB, res.OverheadGB)
	}
	if res.BudgetGB != 43.2 || res.PlanVRAMGB != 48 {
		t.Errorf("budget: got budget_gb=%v plan_vram_gb=%d", res.BudgetGB, res.PlanVRAMGB)
	}
	if res.MaxContextThatFits != 16384 {
		t.Errorf("max_context_that_fits: got %d", res.MaxContextThatFits)
	}
	if res.LimitingFactor != InferenceFitLimitedByKVCache {
		t.Errorf("limiting_factor: got %q", res.LimitingFactor)
	}
	if len(res.Suggestions) != 3 {
		t.Fatalf("expected 3 suggestions, got %d", len(res.Suggestions))
	}
	if res.Suggestions[0].Kind != InferenceFitSuggestionReduceContext || res.Suggestions[0].MaxModelLen != 16384 {
		t.Errorf("reduce_context suggestion: got %+v", res.Suggestions[0])
	}
	if res.Suggestions[2].Kind != InferenceFitSuggestionLargerPlan || res.Suggestions[2].PlanName != "gpu-h100-1" {
		t.Errorf("larger_plan suggestion: got %+v", res.Suggestions[2])
	}
}

func TestGetInferenceServiceUsage_WireFormat(t *testing.T) {
	const raw = `{
		"service_id": "11111111-1111-1111-1111-111111111111",
		"from": "2026-08-01T00:00:00Z",
		"to": "2026-08-02T00:00:00Z",
		"bucket_seconds": 3600,
		"totals": {
			"calls": 120, "errors": 3, "input_tokens": 40000, "output_tokens": 12000,
			"total_tokens": 52000, "cost_microcents": 96000, "images": 0,
			"avg_latency_ms": 480, "p95_latency_ms": 1350, "error_rate": 0.025
		},
		"series": [
			{"bucket_start":"2026-08-01T00:00:00Z","calls":60,"errors":1,"input_tokens":20000,
			 "output_tokens":6000,"total_tokens":26000,"cost_microcents":48000,"images":0,
			 "avg_latency_ms":460,"p95_latency_ms":1200}
		],
		"gpu_hour": {"billed_hours": 24, "hourly_rate_eur": 1.85, "cost_eur": 44.4},
		"month_to_date": {
			"from": "2026-08-01T00:00:00Z",
			"tokens": {
				"calls": 900, "errors": 10, "input_tokens": 300000, "output_tokens": 90000,
				"total_tokens": 390000, "cost_microcents": 720000, "images": 0,
				"avg_latency_ms": 500, "p95_latency_ms": 1400, "error_rate": 0.011
			},
			"gpu_hour": {"billed_hours": 48, "hourly_rate_eur": 1.85, "cost_eur": 88.8}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference-services/svc-1/usage" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("since"); got != "24h" {
			t.Errorf("since query: got %q, want %q", got, "24h")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	u, err := c.GetInferenceServiceUsage(context.Background(), "svc-1", "24h")
	if err != nil {
		t.Fatalf("GetInferenceServiceUsage: %v", err)
	}
	if u.BucketSeconds != 3600 {
		t.Errorf("bucket_seconds: got %d", u.BucketSeconds)
	}
	if u.Totals.TotalTokens != 52000 || u.Totals.CostMicrocents != 96000 {
		t.Errorf("totals: got total_tokens=%d cost_microcents=%d", u.Totals.TotalTokens, u.Totals.CostMicrocents)
	}
	if u.Totals.P95LatencyMs != 1350 {
		t.Errorf("totals.p95_latency_ms: got %d", u.Totals.P95LatencyMs)
	}
	if u.Totals.ErrorRate != 0.025 {
		t.Errorf("totals.error_rate: got %v", u.Totals.ErrorRate)
	}
	if len(u.Series) != 1 || u.Series[0].BucketStart != "2026-08-01T00:00:00Z" {
		t.Errorf("series: got %+v", u.Series)
	}
	if u.GpuHour == nil || u.GpuHour.BilledHours != 24 || u.GpuHour.CostEUR != 44.4 {
		t.Errorf("gpu_hour: got %+v", u.GpuHour)
	}
	if u.MonthToDate == nil {
		t.Fatal("month_to_date: got nil")
	}
	if u.MonthToDate.Tokens.TotalTokens != 390000 {
		t.Errorf("month_to_date.tokens.total_tokens: got %d", u.MonthToDate.Tokens.TotalTokens)
	}
	if u.MonthToDate.GpuHour == nil || u.MonthToDate.GpuHour.CostEUR != 88.8 {
		t.Errorf("month_to_date.gpu_hour: got %+v", u.MonthToDate.GpuHour)
	}
}

// TestGetInferenceServiceUsage_NoSince confirms the query string is omitted
// entirely when no window is asked for, so the API default applies.
func TestGetInferenceServiceUsage_NoSince(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"service_id":"svc-1","bucket_seconds":3600,"totals":{},"series":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if _, err := c.GetInferenceServiceUsage(context.Background(), "svc-1", ""); err != nil {
		t.Fatalf("GetInferenceServiceUsage: %v", err)
	}
}

func TestGetInferenceServiceUsage_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	u, err := c.GetInferenceServiceUsage(context.Background(), "nope", "")
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if u != nil {
		t.Errorf("expected nil usage on 404, got %+v", u)
	}
}

func TestGetInferenceServiceMetrics_WireFormat(t *testing.T) {
	const raw = `{
		"service_id": "11111111-1111-1111-1111-111111111111",
		"from": "2026-08-01T09:30:00Z",
		"to": "2026-08-01T10:00:00Z",
		"snapshots": [
			{
				"collected_at": "2026-08-01T09:59:00Z",
				"model_name": "llama-3.1-8b-instruct",
				"server_reachable": true,
				"requests_running": 3,
				"requests_waiting": 1,
				"gpu_cache_usage_perc": 0.42,
				"generation_tokens_per_sec": 180.5,
				"prompt_tokens_per_sec": 640.25,
				"avg_ttft_ms": 120.5,
				"avg_tpot_ms": 18.25,
				"avg_e2e_latency_ms": 900.75,
				"requests_success_total": 4210,
				"gpus": [
					{"index":0,"util_percent":88.5,"mem_used_mb":40960,"mem_total_mb":49140,"temp_c":64,"power_w":275.5}
				]
			}
		],
		"latest": {
			"collected_at": "2026-08-01T09:59:00Z",
			"server_reachable": true,
			"requests_running": 3,
			"gpu_cache_usage_perc": 0.42
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference-services/svc-1/metrics" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("since"); got != "30m" {
			t.Errorf("since query: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	m, err := c.GetInferenceServiceMetrics(context.Background(), "svc-1", "30m")
	if err != nil {
		t.Fatalf("GetInferenceServiceMetrics: %v", err)
	}
	if len(m.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(m.Snapshots))
	}
	snap := m.Snapshots[0]
	if !snap.ServerReachable {
		t.Error("server_reachable: got false")
	}
	if snap.ModelName != "llama-3.1-8b-instruct" {
		t.Errorf("model_name: got %q", snap.ModelName)
	}
	if snap.RequestsRunning != 3 || snap.RequestsWaiting != 1 {
		t.Errorf("request gauges: got running=%v waiting=%v", snap.RequestsRunning, snap.RequestsWaiting)
	}
	if snap.GPUCacheUsagePerc != 0.42 {
		t.Errorf("gpu_cache_usage_perc: got %v", snap.GPUCacheUsagePerc)
	}
	if snap.GenerationTokensPerSec != 180.5 || snap.PromptTokensPerSec != 640.25 {
		t.Errorf("throughput: got gen=%v prompt=%v", snap.GenerationTokensPerSec, snap.PromptTokensPerSec)
	}
	if snap.AvgTimeToFirstTokenMs != 120.5 {
		t.Errorf("avg_ttft_ms: got %v (check the json tag)", snap.AvgTimeToFirstTokenMs)
	}
	if snap.AvgTimePerOutputTokenMs != 18.25 {
		t.Errorf("avg_tpot_ms: got %v (check the json tag)", snap.AvgTimePerOutputTokenMs)
	}
	if snap.AvgE2ELatencyMs != 900.75 {
		t.Errorf("avg_e2e_latency_ms: got %v", snap.AvgE2ELatencyMs)
	}
	if snap.RequestsSuccessTotal != 4210 {
		t.Errorf("requests_success_total: got %v", snap.RequestsSuccessTotal)
	}
	if len(snap.GPUs) != 1 {
		t.Fatalf("expected 1 gpu, got %d", len(snap.GPUs))
	}
	if snap.GPUs[0].UtilPercent != 88.5 || snap.GPUs[0].MemTotalMB != 49140 || snap.GPUs[0].PowerW != 275.5 {
		t.Errorf("gpu stats: got %+v", snap.GPUs[0])
	}
	if m.Latest == nil || m.Latest.RequestsRunning != 3 {
		t.Errorf("latest: got %+v", m.Latest)
	}
}

func TestGetInferenceServiceMetrics_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	m, err := c.GetInferenceServiceMetrics(context.Background(), "nope", "")
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if m != nil {
		t.Errorf("expected nil metrics on 404, got %+v", m)
	}
}

const rawInferenceAdapterJSON = `{
  "id": "44444444-4444-4444-4444-444444444444",
  "organization_id": "33333333-3333-3333-3333-333333333333",
  "inference_service_id": "11111111-1111-1111-1111-111111111111",
  "base_model_id": "llama-3.1-8b-instruct",
  "served_model_name": "support-tone-v3",
  "version": 3,
  "files_bucket": "org-artifacts",
  "files_key_prefix": "adapters/support-tone/v3/",
  "adapter_sha256": "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
  "size_bytes": 134217728,
  "base_model_license": "llama3.1",
  "status": "active",
  "created_at": "2026-08-01T08:00:00Z",
  "promoted_at": "2026-08-01T09:00:00Z",
  "deleted_at": null
}`

func TestInferenceModelAdapter_WireFormat(t *testing.T) {
	var a InferenceModelAdapter
	if err := json.Unmarshal([]byte(rawInferenceAdapterJSON), &a); err != nil {
		t.Fatalf("unmarshal adapter: %v", err)
	}
	if a.ID != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("id: got %q", a.ID)
	}
	if a.InferenceServiceID == nil || *a.InferenceServiceID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("inference_service_id: got %v", a.InferenceServiceID)
	}
	if a.BaseModelID != "llama-3.1-8b-instruct" {
		t.Errorf("base_model_id: got %q", a.BaseModelID)
	}
	if a.ServedModelName != "support-tone-v3" {
		t.Errorf("served_model_name: got %q", a.ServedModelName)
	}
	if a.Version != 3 {
		t.Errorf("version: got %d", a.Version)
	}
	if a.FilesBucket != "org-artifacts" || a.FilesKeyPrefix != "adapters/support-tone/v3/" {
		t.Errorf("files location: got bucket=%q prefix=%q", a.FilesBucket, a.FilesKeyPrefix)
	}
	if a.SizeBytes != 134217728 {
		t.Errorf("size_bytes: got %d", a.SizeBytes)
	}
	if a.Status != InferenceAdapterStatusActive {
		t.Errorf("status: got %q", a.Status)
	}
	if a.PromotedAt == nil || *a.PromotedAt != "2026-08-01T09:00:00Z" {
		t.Errorf("promoted_at: got %v", a.PromotedAt)
	}
	if a.DeletedAt != nil {
		t.Errorf("deleted_at: expected nil, got %v", a.DeletedAt)
	}
}

func TestListInferenceServiceAdapters_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference-services/svc-1/adapters" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"adapters":[` + rawInferenceAdapterJSON + `]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	adapters, err := c.ListInferenceServiceAdapters(context.Background(), "svc-1")
	if err != nil {
		t.Fatalf("ListInferenceServiceAdapters: %v", err)
	}
	if len(adapters) != 1 || adapters[0].ServedModelName != "support-tone-v3" {
		t.Errorf("adapters: got %+v (check the `adapters` envelope tag)", adapters)
	}
}

func TestListInferenceServiceAdapters_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	adapters, err := c.ListInferenceServiceAdapters(context.Background(), "nope")
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if adapters != nil {
		t.Errorf("expected nil adapters on 404, got %+v", adapters)
	}
}

// TestPromoteAndDemoteInferenceAdapter_RequestShape pins the two opposite
// transitions: both POST with no body and both answer with the `adapter`
// envelope.
func TestPromoteAndDemoteInferenceAdapter_RequestShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wantPath string
		call     func(*Client) (*InferenceModelAdapter, error)
	}{
		{
			name:     "promote",
			wantPath: "/inference-services/svc-1/adapters/ad-1/promote",
			call: func(c *Client) (*InferenceModelAdapter, error) {
				return c.PromoteInferenceAdapter(context.Background(), "svc-1", "ad-1")
			},
		},
		{
			name:     "demote",
			wantPath: "/inference-services/svc-1/adapters/ad-1/demote",
			call: func(c *Client) (*InferenceModelAdapter, error) {
				return c.DemoteInferenceAdapter(context.Background(), "svc-1", "ad-1")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.URL.Path != tc.wantPath {
					t.Errorf("unexpected path %s, want %s", r.URL.Path, tc.wantPath)
				}
				if r.ContentLength > 0 {
					t.Errorf("expected an empty body, got %d bytes", r.ContentLength)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"adapter":` + rawInferenceAdapterJSON + `}`))
			}))
			defer srv.Close()

			a, err := tc.call(newTestClient(srv.URL))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if a == nil || a.ID != "44444444-4444-4444-4444-444444444444" {
				t.Errorf("decoded adapter: got %+v (check the `adapter` envelope tag)", a)
			}
		})
	}
}

func TestRegisterInferenceAdapter_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services/adapters" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		for _, key := range []string{
			"base_model_id", "served_model_name", "version",
			"files_bucket", "files_key_prefix", "adapter_sha256", "size_bytes",
		} {
			if _, ok := body[key]; !ok {
				t.Errorf("request body missing field %q (json tag regression)", key)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"adapter":` + rawInferenceAdapterJSON + `}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	a, err := c.RegisterInferenceAdapter(context.Background(), InferenceAdapterRegisterRequest{
		BaseModelID:     "llama-3.1-8b-instruct",
		ServedModelName: "support-tone-v3",
		Version:         3,
		FilesBucket:     "org-artifacts",
		FilesKeyPrefix:  "adapters/support-tone/v3/",
		AdapterSHA256:   "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
		SizeBytes:       134217728,
	})
	if err != nil {
		t.Fatalf("RegisterInferenceAdapter: %v", err)
	}
	if a == nil || a.Version != 3 {
		t.Errorf("decoded adapter: got %+v", a)
	}
}

// TestDeleteInferenceAdapter_ActiveIsRefused confirms the 409 on an actively
// served version surfaces as an APIError rather than being swallowed.
func TestDeleteInferenceAdapter_ActiveIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/inference-services/adapters/ad-1" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"adapter is actively served"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	err := c.DeleteInferenceAdapter(context.Background(), "ad-1")
	if err == nil {
		t.Fatal("expected an error on 409, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d", apiErr.StatusCode)
	}
}
