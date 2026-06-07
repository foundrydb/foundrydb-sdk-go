package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DataPipelineType identifies a data pipeline topology.
type DataPipelineType string

// DataPipelineTypeCDCPGToKafka streams CDC events from a PostgreSQL source
// into Kafka topics via a Debezium connector on the sink's Kafka Connect addon.
const DataPipelineTypeCDCPGToKafka DataPipelineType = "cdc_pg_to_kafka"

// DataPipelineConfig is the optional configuration for a data pipeline.
type DataPipelineConfig struct {
	DatabaseName string   `json:"database_name,omitempty"`
	Tables       []string `json:"tables,omitempty"`
	TopicPrefix  string   `json:"topic_prefix,omitempty"`
	SnapshotMode string   `json:"snapshot_mode,omitempty"`
}

// DataPipeline is a data flow between two managed services.
type DataPipeline struct {
	ID                 string             `json:"id"`
	OrganizationID     string             `json:"organization_id"`
	Name               string             `json:"name"`
	PipelineType       DataPipelineType   `json:"pipeline_type"`
	SourceServiceID    string             `json:"source_service_id"`
	SinkServiceID      string             `json:"sink_service_id"`
	Status             string             `json:"status"`
	ProvisionStep      *string            `json:"provision_step,omitempty"`
	Config             DataPipelineConfig `json:"config"`
	ConnectorName      *string            `json:"connector_name,omitempty"`
	PublicationName    *string            `json:"publication_name,omitempty"`
	SlotName           *string            `json:"slot_name,omitempty"`
	TopicPrefix        *string            `json:"topic_prefix,omitempty"`
	LastConnectorState *string            `json:"last_connector_state,omitempty"`
	SourceLagBytes     *int64             `json:"source_lag_bytes,omitempty"`
	LastHealthCheckAt  *string            `json:"last_health_check_at,omitempty"`
	ErrorMessage       *string            `json:"error_message,omitempty"`
	CreatedAt          string             `json:"created_at"`
	UpdatedAt          string             `json:"updated_at"`
}

// CreateDataPipelineRequest is the body for CreateDataPipeline.
type CreateDataPipelineRequest struct {
	Name            string             `json:"name"`
	PipelineType    DataPipelineType   `json:"pipeline_type"`
	SourceServiceID string             `json:"source_service_id"`
	SinkServiceID   string             `json:"sink_service_id"`
	Config          DataPipelineConfig `json:"config,omitempty"`
}

// DataPipelineStatus is the response from GetDataPipelineStatus.
type DataPipelineStatus struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	ConnectorName     *string           `json:"connector_name,omitempty"`
	ConnectorState    *string           `json:"connector_state,omitempty"`
	TaskStates        json.RawMessage   `json:"task_states,omitempty"`
	SourceLagBytes    *int64            `json:"source_lag_bytes,omitempty"`
	TopicPrefix       *string           `json:"topic_prefix,omitempty"`
	LastHealthCheckAt *string           `json:"last_health_check_at,omitempty"`
	ErrorMessage      *string           `json:"error_message,omitempty"`
}

type listDataPipelinesResponse struct {
	Pipelines []DataPipeline `json:"pipelines"`
}

// CreateDataPipeline creates a data pipeline between two services owned by the
// organization. Provisioning is asynchronous: the returned pipeline is in the
// Pending state. Poll GetDataPipelineStatus until it reaches Running.
func (c *Client) CreateDataPipeline(ctx context.Context, orgID string, req CreateDataPipelineRequest) (*DataPipeline, error) {
	resp, err := c.do(ctx, http.MethodPost, "/organizations/"+orgID+"/pipelines", req, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var p DataPipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateDataPipeline response: %w", err)
	}
	return &p, nil
}

// ListDataPipelines returns all data pipelines owned by the organization.
func (c *Client) ListDataPipelines(ctx context.Context, orgID string) ([]DataPipeline, error) {
	resp, err := c.do(ctx, http.MethodGet, "/organizations/"+orgID+"/pipelines", nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listDataPipelinesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListDataPipelines response: %w", err)
	}
	return result.Pipelines, nil
}

// GetDataPipeline returns the data pipeline with the given ID.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetDataPipeline(ctx context.Context, orgID, pipelineID string) (*DataPipeline, error) {
	resp, err := c.do(ctx, http.MethodGet, "/organizations/"+orgID+"/pipelines/"+pipelineID, nil, orgID)
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
	var p DataPipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetDataPipeline response: %w", err)
	}
	return &p, nil
}

// DeleteDataPipeline schedules asynchronous teardown of the data pipeline.
func (c *Client) DeleteDataPipeline(ctx context.Context, orgID, pipelineID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/organizations/"+orgID+"/pipelines/"+pipelineID, nil, orgID)
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// GetDataPipelineStatus returns the latest reconciler-observed status of the
// pipeline, including connector state, per-task states, and source lag.
func (c *Client) GetDataPipelineStatus(ctx context.Context, pipelineID string) (*DataPipelineStatus, error) {
	resp, err := c.do(ctx, http.MethodGet, "/pipelines/"+pipelineID+"/status", nil, "")
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
	var s DataPipelineStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetDataPipelineStatus response: %w", err)
	}
	return &s, nil
}
