package foundrydb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rawInferenceKeyJSON is a verbatim sample of the wire format the platform
// returns for a data-plane key, including the optional service scope.
const rawInferenceKeyJSON = `{
  "id": "55555555-5555-5555-5555-555555555555",
  "name": "checkout-app",
  "key_prefix": "fdb-inf-a1b2c3",
  "monthly_token_limit": 1000000,
  "rate_limit_rpm": 120,
  "status": "active",
  "tokens_used_cycle": 42000,
  "cycle_month": "2026-08-01T00:00:00Z",
  "service_id": "11111111-1111-1111-1111-111111111111",
  "created_at": "2026-08-01T10:00:00Z",
  "revoked_at": null
}`

func TestInferenceKey_ServiceScope_WireFormat(t *testing.T) {
	var k InferenceKey
	if err := json.Unmarshal([]byte(rawInferenceKeyJSON), &k); err != nil {
		t.Fatalf("unmarshal inference key: %v", err)
	}
	if k.ServiceID == nil || *k.ServiceID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("service_id: got %v (check the `service_id` json tag)", k.ServiceID)
	}
	if k.RateLimitRPM != 120 {
		t.Errorf("rate_limit_rpm: got %d", k.RateLimitRPM)
	}
	if k.RevokedAt != nil {
		t.Errorf("revoked_at: expected nil, got %v", k.RevokedAt)
	}

	// An org-scoped key carries no service_id at all, and must decode as nil
	// rather than as the empty string.
	var orgScoped InferenceKey
	if err := json.Unmarshal([]byte(`{"id":"x","name":"n","key_prefix":"p"}`), &orgScoped); err != nil {
		t.Fatalf("unmarshal org-scoped key: %v", err)
	}
	if orgScoped.ServiceID != nil {
		t.Errorf("service_id on an org-scoped key: expected nil, got %v", orgScoped.ServiceID)
	}
}

// TestCreateInferenceKeyRequest_ServiceScopeOmitted pins that an unset service
// scope is omitted from the body entirely: sending an empty service_id would
// read as a scope to nothing rather than as an org-scoped key.
func TestCreateInferenceKeyRequest_ServiceScopeOmitted(t *testing.T) {
	b, err := json.Marshal(CreateInferenceKeyRequest{Name: "checkout-app", MonthlyTokenLimit: 1000000})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := m["service_id"]; ok {
		t.Error("service_id must be omitted when the key is org-scoped")
	}
	if _, ok := m["rate_limit_rpm"]; ok {
		t.Error("rate_limit_rpm must be omitted when unset so the platform default applies")
	}

	svcID := "11111111-1111-1111-1111-111111111111"
	rpm := 120
	b, err = json.Marshal(CreateInferenceKeyRequest{
		Name: "checkout-app", MonthlyTokenLimit: 1000000,
		RateLimitRPM: &rpm, ServiceID: &svcID,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m = map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if m["service_id"] != svcID {
		t.Errorf("service_id: got %v", m["service_id"])
	}
	if m["rate_limit_rpm"] != float64(rpm) {
		t.Errorf("rate_limit_rpm: got %v", m["rate_limit_rpm"])
	}
}

// TestCreateInferenceKey_ActivationNote confirms the advisory note travels back
// with the one-time secret, so callers can warn about the reconcile delay
// instead of treating an immediate invalid_key as a mint failure.
func TestCreateInferenceKey_ActivationNote(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	const note = "The key reaches the inference endpoint within a few seconds; retry an immediate invalid_key."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/organizations/"+orgID+"/inference/keys" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Active-Org-ID"); got != orgID {
			t.Errorf("expected X-Active-Org-ID %q, got %q", orgID, got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"key":` + rawInferenceKeyJSON + `,"secret":"fdb-inf-a1b2c3-secret","activation_note":"` + note + `"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	res, err := c.CreateInferenceKey(context.Background(), orgID, CreateInferenceKeyRequest{
		Name: "checkout-app", MonthlyTokenLimit: 1000000,
	})
	if err != nil {
		t.Fatalf("CreateInferenceKey: %v", err)
	}
	if res.Secret != "fdb-inf-a1b2c3-secret" {
		t.Errorf("secret: got %q", res.Secret)
	}
	if res.ActivationNote != note {
		t.Errorf("activation_note: got %q (check the `activation_note` json tag)", res.ActivationNote)
	}
	if res.Key.ServiceID == nil {
		t.Error("key.service_id: expected the scope to decode")
	}
}

// TestGetInferenceUsage_FreeTier pins the free allowance standing carried
// alongside the usage rows. It describes the current calendar month whatever
// window was asked for.
func TestGetInferenceUsage_FreeTier(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	const raw = `{
		"from": "2026-08-01T00:00:00Z",
		"to": "2026-08-18T00:00:00Z",
		"group_by": "model",
		"rows": [
			{"group_key":"llama-3.1-8b-instruct","provider":"foundrydb_managed","calls":120,
			 "input_tokens":40000,"output_tokens":12000,"total_tokens":52000,"cost_microcents":0}
		],
		"free_tier": {
			"cycle_month": "2026-08-01T00:00:00Z",
			"monthly_tokens": 1000000,
			"tokens_used": 52000,
			"tokens_remaining": 948000
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/organizations/"+orgID+"/inference/usage" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("group_by"); got != "model" {
			t.Errorf("group_by query: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	u, err := c.GetInferenceUsage(context.Background(), orgID, InferenceUsageOptions{GroupBy: "model"})
	if err != nil {
		t.Fatalf("GetInferenceUsage: %v", err)
	}
	if len(u.Rows) != 1 || u.Rows[0].TotalTokens != 52000 {
		t.Errorf("rows: got %+v", u.Rows)
	}
	if u.FreeTier == nil {
		t.Fatal("free_tier: got nil")
	}
	if u.FreeTier.CycleMonth != "2026-08-01T00:00:00Z" {
		t.Errorf("free_tier.cycle_month: got %q", u.FreeTier.CycleMonth)
	}
	if u.FreeTier.MonthlyTokens != 1000000 {
		t.Errorf("free_tier.monthly_tokens: got %d", u.FreeTier.MonthlyTokens)
	}
	if u.FreeTier.TokensUsed != 52000 || u.FreeTier.TokensRemaining != 948000 {
		t.Errorf("free_tier drawdown: got used=%d remaining=%d", u.FreeTier.TokensUsed, u.FreeTier.TokensRemaining)
	}

	// An organization with no allowance standing still answers with its rows.
	var noStanding InferenceUsageSummary
	if err := json.Unmarshal([]byte(`{"from":"a","to":"b","group_by":"model","rows":[]}`), &noStanding); err != nil {
		t.Fatalf("unmarshal usage without free_tier: %v", err)
	}
	if noStanding.FreeTier != nil {
		t.Errorf("free_tier: expected nil when absent, got %+v", noStanding.FreeTier)
	}
}

const rawProviderChainJSON = `{
  "provider_chain": ["foundrydb_managed", "mistral", "none"],
  "fully_eu_resident": true,
  "overrides": [
    {
      "organization_id": "33333333-3333-3333-3333-333333333333",
      "surface": "chat",
      "provider_chain": ["mistral"],
      "created_at": "2026-08-01T10:00:00Z",
      "updated_at": "2026-08-01T10:00:00Z"
    }
  ]
}`

func TestGetInferenceProviderChain_WireFormat(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/organizations/"+orgID+"/inference/chain" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Active-Org-ID"); got != orgID {
			t.Errorf("expected X-Active-Org-ID %q, got %q", orgID, got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rawProviderChainJSON))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	info, err := c.GetInferenceProviderChain(context.Background(), orgID)
	if err != nil {
		t.Fatalf("GetInferenceProviderChain: %v", err)
	}
	if len(info.ProviderChain) != 3 || info.ProviderChain[0] != "foundrydb_managed" {
		t.Errorf("provider_chain: got %v", info.ProviderChain)
	}
	if info.ProviderChain[2] != "none" {
		t.Errorf("chain terminator: got %q, want the literal \"none\"", info.ProviderChain[2])
	}
	if !info.FullyEUResident {
		t.Error("fully_eu_resident: got false")
	}
	if len(info.Overrides) != 1 {
		t.Fatalf("expected 1 override, got %d", len(info.Overrides))
	}
	if info.Overrides[0].Surface != "chat" {
		t.Errorf("overrides[0].surface: got %q", info.Overrides[0].Surface)
	}
	if len(info.Overrides[0].ProviderChain) != 1 || info.Overrides[0].ProviderChain[0] != "mistral" {
		t.Errorf("overrides[0].provider_chain: got %v", info.Overrides[0].ProviderChain)
	}
}

func TestSetInferenceProviderChain_RequestShape(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/organizations/"+orgID+"/inference/chain" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		chain, ok := body["provider_chain"].([]any)
		if !ok {
			t.Fatalf("provider_chain: got %T (json tag regression)", body["provider_chain"])
		}
		if len(chain) != 3 || chain[0] != "foundrydb_managed" || chain[2] != "none" {
			t.Errorf("provider_chain: got %v", chain)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rawProviderChainJSON))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	info, err := c.SetInferenceProviderChain(context.Background(), orgID, []string{"foundrydb_managed", "mistral", "none"})
	if err != nil {
		t.Fatalf("SetInferenceProviderChain: %v", err)
	}
	// The per-surface overrides are untouched by an org-level replace and are
	// echoed back with it.
	if len(info.Overrides) != 1 {
		t.Errorf("overrides: got %+v", info.Overrides)
	}
}

func TestSetInferenceSurfaceOverride_RequestShape(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/organizations/"+orgID+"/inference/chain/overrides/chat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"organization_id":"` + orgID + `","surface":"chat","provider_chain":["mistral"],
			"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ov, err := c.SetInferenceSurfaceOverride(context.Background(), orgID, "chat", []string{"mistral"})
	if err != nil {
		t.Fatalf("SetInferenceSurfaceOverride: %v", err)
	}
	if ov.Surface != "chat" {
		t.Errorf("surface: got %q", ov.Surface)
	}
	if len(ov.ProviderChain) != 1 || ov.ProviderChain[0] != "mistral" {
		t.Errorf("provider_chain: got %v", ov.ProviderChain)
	}
}

// TestInferenceSurfaceOverride_PathEscaped confirms the surface name is escaped
// into the path, so an unexpected value cannot walk out of the overrides
// collection.
func TestInferenceSurfaceOverride_PathEscaped(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/organizations/" + orgID + "/inference/chain/overrides/chat%2F..%2Fsettings"
		if r.URL.EscapedPath() != want {
			t.Errorf("escaped path: got %q, want %q", r.URL.EscapedPath(), want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.DeleteInferenceSurfaceOverride(context.Background(), orgID, "chat/../settings"); err != nil {
		t.Fatalf("DeleteInferenceSurfaceOverride: %v", err)
	}
}

// TestDeleteInferenceSurfaceOverride_Idempotent confirms deleting an absent
// override succeeds.
func TestDeleteInferenceSurfaceOverride_Idempotent(t *testing.T) {
	const orgID = "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/organizations/"+orgID+"/inference/chain/overrides/advisor" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.DeleteInferenceSurfaceOverride(context.Background(), orgID, "advisor"); err != nil {
		t.Fatalf("DeleteInferenceSurfaceOverride: %v", err)
	}
}
