package foundrydb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rawDataPipelineJSON is a verbatim sample of the wire format the platform
// returns for a data pipeline. Tests decode THIS (not a struct round-trip) so
// a silently-wrong json tag is caught: round-trip tests would re-encode with
// the same wrong tag and pass. (See the Service.ID uuid->id regression.)
const rawDataPipelineJSON = `{
  "id": "11111111-1111-1111-1111-111111111111",
  "organization_id": "22222222-2222-2222-2222-222222222222",
  "name": "orders-to-kafka",
  "pipeline_type": "cdc_pg_to_kafka",
  "source_service_id": "33333333-3333-3333-3333-333333333333",
  "sink_service_id": "44444444-4444-4444-4444-444444444444",
  "status": "Running",
  "provision_step": "create_connector",
  "config": {
    "database_name": "defaultdb",
    "tables": ["public.orders"],
    "topic_prefix": "shop",
    "snapshot_mode": "initial"
  },
  "connector_name": "mdb-pipeline-11111111",
  "publication_name": "mdb_pipeline_11111111",
  "slot_name": "mdb_pipeline_11111111",
  "topic_prefix": "shop",
  "last_connector_state": "RUNNING",
  "source_lag_bytes": 4096,
  "last_health_check_at": "2026-06-07T12:00:00Z",
  "error_message": null,
  "created_at": "2026-06-07T11:00:00Z",
  "updated_at": "2026-06-07T12:00:00Z"
}`

func TestDataPipeline_WireFormat(t *testing.T) {
	var p DataPipeline
	if err := json.Unmarshal([]byte(rawDataPipelineJSON), &p); err != nil {
		t.Fatalf("unmarshal data pipeline: %v", err)
	}

	if p.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("id: got %q (check the `id` json tag)", p.ID)
	}
	if p.OrganizationID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("organization_id: got %q", p.OrganizationID)
	}
	if p.Name != "orders-to-kafka" {
		t.Errorf("name: got %q", p.Name)
	}
	if p.PipelineType != DataPipelineTypeCDCPGToKafka {
		t.Errorf("pipeline_type: got %q, want %q", p.PipelineType, DataPipelineTypeCDCPGToKafka)
	}
	if p.SourceServiceID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("source_service_id: got %q", p.SourceServiceID)
	}
	if p.SinkServiceID != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("sink_service_id: got %q", p.SinkServiceID)
	}
	if p.Status != "Running" {
		t.Errorf("status: got %q", p.Status)
	}
	if p.ProvisionStep == nil || *p.ProvisionStep != "create_connector" {
		t.Errorf("provision_step: got %v", p.ProvisionStep)
	}
	if p.Config.DatabaseName != "defaultdb" {
		t.Errorf("config.database_name: got %q", p.Config.DatabaseName)
	}
	if len(p.Config.Tables) != 1 || p.Config.Tables[0] != "public.orders" {
		t.Errorf("config.tables: got %v", p.Config.Tables)
	}
	if p.Config.TopicPrefix != "shop" {
		t.Errorf("config.topic_prefix: got %q", p.Config.TopicPrefix)
	}
	if p.ConnectorName == nil || *p.ConnectorName != "mdb-pipeline-11111111" {
		t.Errorf("connector_name: got %v", p.ConnectorName)
	}
	if p.SlotName == nil || *p.SlotName != "mdb_pipeline_11111111" {
		t.Errorf("slot_name: got %v", p.SlotName)
	}
	if p.LastConnectorState == nil || *p.LastConnectorState != "RUNNING" {
		t.Errorf("last_connector_state: got %v", p.LastConnectorState)
	}
	if p.SourceLagBytes == nil || *p.SourceLagBytes != 4096 {
		t.Errorf("source_lag_bytes: got %v", p.SourceLagBytes)
	}
	if p.ErrorMessage != nil {
		t.Errorf("error_message: expected nil, got %v", p.ErrorMessage)
	}
}

// TestCreateDataPipelineRequest_WireFormat pins the request body field names
// the platform expects.
func TestCreateDataPipelineRequest_WireFormat(t *testing.T) {
	req := CreateDataPipelineRequest{
		Name:            "orders-to-kafka",
		PipelineType:    DataPipelineTypeCDCPGToKafka,
		SourceServiceID: "src",
		SinkServiceID:   "sink",
		Config:          DataPipelineConfig{Tables: []string{"public.orders"}, TopicPrefix: "shop"},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"name", "pipeline_type", "source_service_id", "sink_service_id", "config"} {
		if _, ok := m[key]; !ok {
			t.Errorf("request body missing field %q (json tag regression)", key)
		}
	}
	if m["pipeline_type"] != "cdc_pg_to_kafka" {
		t.Errorf("pipeline_type serialized as %v", m["pipeline_type"])
	}
}

func TestDataPipelineStatus_WireFormat(t *testing.T) {
	const raw = `{
		"id": "11111111-1111-1111-1111-111111111111",
		"status": "Running",
		"connector_name": "mdb-pipeline-11111111",
		"connector_state": "RUNNING",
		"task_states": [{"id": 0, "state": "RUNNING"}],
		"source_lag_bytes": 0,
		"topic_prefix": "shop",
		"last_health_check_at": "2026-06-07T12:00:00Z",
		"error_message": null
	}`
	var s DataPipelineStatus
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if s.Status != "Running" {
		t.Errorf("status: got %q", s.Status)
	}
	if s.ConnectorState == nil || *s.ConnectorState != "RUNNING" {
		t.Errorf("connector_state: got %v", s.ConnectorState)
	}
	if s.SourceLagBytes == nil || *s.SourceLagBytes != 0 {
		t.Errorf("source_lag_bytes: got %v", s.SourceLagBytes)
	}
	if len(s.TaskStates) == 0 {
		t.Error("task_states: expected raw JSON to be retained")
	}
}

// TestCreateDataPipeline_RequestShape verifies the client targets the
// org-scoped path, sends the org header, and uses POST.
func TestCreateDataPipeline_RequestShape(t *testing.T) {
	const orgID = "22222222-2222-2222-2222-222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/organizations/"+orgID+"/pipelines" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Active-Org-ID"); got != orgID {
			t.Errorf("expected X-Active-Org-ID %q, got %q", orgID, got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(rawDataPipelineJSON))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	p, err := c.CreateDataPipeline(context.Background(), orgID, CreateDataPipelineRequest{
		Name: "orders-to-kafka", PipelineType: DataPipelineTypeCDCPGToKafka,
		SourceServiceID: "src", SinkServiceID: "sink",
	})
	if err != nil {
		t.Fatalf("CreateDataPipeline: %v", err)
	}
	if p.Name != "orders-to-kafka" {
		t.Errorf("decoded name: got %q", p.Name)
	}
}

// TestGetDataPipelineStatus_NotFound confirms 404 maps to nil, nil.
func TestGetDataPipelineStatus_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	s, err := c.GetDataPipelineStatus(context.Background(), "nope")
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if s != nil {
		t.Errorf("expected nil status on 404, got %+v", s)
	}
}
