package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Embedding pipelines: managed auto-vectorization for PostgreSQL services
// with pgvector. A pipeline watches a source table, embeds the configured
// text columns through the customer's own provider key, and writes vectors
// into an indexed companion table. Continuous pipelines process rows as they
// change; scheduled and manual pipelines process in discrete runs.

// EmbeddingPipelineMode selects how a pipeline processes its source table.
type EmbeddingPipelineMode string

const (
	EmbeddingPipelineModeContinuous EmbeddingPipelineMode = "continuous"
	EmbeddingPipelineModeScheduled  EmbeddingPipelineMode = "scheduled"
	EmbeddingPipelineModeManual     EmbeddingPipelineMode = "manual"
)

// EmbeddingPipeline is one auto-vectorization pipeline on a managed service.
type EmbeddingPipeline struct {
	ID                  string                `json:"id"`
	ServiceID           string                `json:"service_id"`
	DatabaseName        string                `json:"database_name"`
	SourceSchema        string                `json:"source_schema"`
	SourceTable         string                `json:"source_table"`
	TextColumns         []string              `json:"text_columns"`
	ModelProvider       string                `json:"model_provider"`
	EmbeddingModel      string                `json:"embedding_model"`
	ModelDimensions     int                   `json:"model_dimensions"`
	TargetSchema        string                `json:"target_schema"`
	TargetTable         string                `json:"target_table"`
	ProviderBaseURL     *string               `json:"provider_base_url,omitempty"`
	BatchSize           int                   `json:"batch_size"`
	PollIntervalSeconds int                   `json:"poll_interval_seconds"`
	Mode                EmbeddingPipelineMode `json:"mode"`
	ScheduleCron        *string               `json:"schedule_cron,omitempty"`
	SourceFilter        *string               `json:"source_filter,omitempty"`
	MaxRowRetries       int                   `json:"max_row_retries"`
	NextRunAt           *string               `json:"next_run_at,omitempty"`
	Status              string                `json:"status"`
	ErrorMessage        *string               `json:"error_message,omitempty"`
	RowsProcessed       int64                 `json:"rows_processed"`
	RowsPending         int64                 `json:"rows_pending"`
	TokensUsed          int64                 `json:"tokens_used"`
	LastProcessedAt     *string               `json:"last_processed_at,omitempty"`
	LastError           *string               `json:"last_error,omitempty"`
	CreatedAt           string                `json:"created_at"`
	UpdatedAt           string                `json:"updated_at"`
}

// CreateEmbeddingPipelineRequest is the body for CreateEmbeddingPipeline.
type CreateEmbeddingPipelineRequest struct {
	DatabaseName        string                 `json:"database_name"`
	SourceSchema        string                 `json:"source_schema,omitempty"`
	SourceTable         string                 `json:"source_table"`
	TextColumns         []string               `json:"text_columns"`
	ModelProvider       string                 `json:"model_provider"`
	EmbeddingModel      string                 `json:"embedding_model"`
	ModelDimensions     int                    `json:"model_dimensions"`
	TargetTable         string                 `json:"target_table,omitempty"`
	TargetSchema        string                 `json:"target_schema,omitempty"`
	ProviderAPIKey      string                 `json:"provider_api_key"`
	ProviderBaseURL     *string                `json:"provider_base_url,omitempty"`
	BatchSize           *int                   `json:"batch_size,omitempty"`
	PollIntervalSeconds *int                   `json:"poll_interval_seconds,omitempty"`
	Mode                *EmbeddingPipelineMode `json:"mode,omitempty"`
	ScheduleCron        *string                `json:"schedule_cron,omitempty"`
	SourceFilter        *string                `json:"source_filter,omitempty"`
	MaxRowRetries       *int                   `json:"max_row_retries,omitempty"`
}

// UpdateEmbeddingPipelineRequest is the body for UpdateEmbeddingPipeline.
// Only the set fields are changed.
type UpdateEmbeddingPipelineRequest struct {
	EmbeddingModel      *string                `json:"embedding_model,omitempty"`
	ModelDimensions     *int                   `json:"model_dimensions,omitempty"`
	ProviderAPIKey      *string                `json:"provider_api_key,omitempty"`
	ProviderBaseURL     *string                `json:"provider_base_url,omitempty"`
	BatchSize           *int                   `json:"batch_size,omitempty"`
	PollIntervalSeconds *int                   `json:"poll_interval_seconds,omitempty"`
	Mode                *EmbeddingPipelineMode `json:"mode,omitempty"`
	ScheduleCron        *string                `json:"schedule_cron,omitempty"`
	SourceFilter        *string                `json:"source_filter,omitempty"`
	MaxRowRetries       *int                   `json:"max_row_retries,omitempty"`
}

// EmbeddingRunErrorSample records one failed source row of a run.
type EmbeddingRunErrorSample struct {
	SourceRowID string `json:"source_row_id"`
	Error       string `json:"error"`
}

// EmbeddingPipelineRun is one discrete embedding job execution for a
// scheduled or manual pipeline.
type EmbeddingPipelineRun struct {
	ID           string                    `json:"id"`
	PipelineID   string                    `json:"pipeline_id"`
	Status       string                    `json:"status"`
	Trigger      string                    `json:"trigger"`
	StartedAt    *string                   `json:"started_at,omitempty"`
	FinishedAt   *string                   `json:"finished_at,omitempty"`
	RowsScanned  int64                     `json:"rows_scanned"`
	RowsEmbedded int64                     `json:"rows_embedded"`
	RowsFailed   int64                     `json:"rows_failed"`
	TokensUsed   int64                     `json:"tokens_used"`
	ErrorMessage *string                   `json:"error_message,omitempty"`
	ErrorSample  []EmbeddingRunErrorSample `json:"error_sample,omitempty"`
	CreatedAt    string                    `json:"created_at"`
}

type listEmbeddingPipelinesResponse struct {
	Pipelines []EmbeddingPipeline `json:"pipelines"`
}

type listEmbeddingPipelineRunsResponse struct {
	Runs []EmbeddingPipelineRun `json:"runs"`
}

func embeddingPipelinesPath(serviceID string) string {
	return "/managed-services/" + serviceID + "/embedding-pipelines"
}

// ListEmbeddingPipelines returns all embedding pipelines on the service.
func (c *Client) ListEmbeddingPipelines(ctx context.Context, serviceID string) ([]EmbeddingPipeline, error) {
	resp, err := c.do(ctx, http.MethodGet, embeddingPipelinesPath(serviceID), nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listEmbeddingPipelinesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListEmbeddingPipelines response: %w", err)
	}
	return result.Pipelines, nil
}

// CreateEmbeddingPipeline creates an embedding pipeline on a PostgreSQL
// service with pgvector. Setup is asynchronous: the returned pipeline starts
// in the configuring status; poll GetEmbeddingPipeline until it is active.
func (c *Client) CreateEmbeddingPipeline(ctx context.Context, serviceID string, req CreateEmbeddingPipelineRequest) (*EmbeddingPipeline, error) {
	resp, err := c.do(ctx, http.MethodPost, embeddingPipelinesPath(serviceID), req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var p EmbeddingPipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateEmbeddingPipeline response: %w", err)
	}
	return &p, nil
}

// GetEmbeddingPipeline returns one embedding pipeline.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetEmbeddingPipeline(ctx context.Context, serviceID, pipelineID string) (*EmbeddingPipeline, error) {
	resp, err := c.do(ctx, http.MethodGet, embeddingPipelinesPath(serviceID)+"/"+pipelineID, nil, "")
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
	var p EmbeddingPipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetEmbeddingPipeline response: %w", err)
	}
	return &p, nil
}

// UpdateEmbeddingPipeline updates the set fields of an embedding pipeline.
func (c *Client) UpdateEmbeddingPipeline(ctx context.Context, serviceID, pipelineID string, req UpdateEmbeddingPipelineRequest) (*EmbeddingPipeline, error) {
	resp, err := c.do(ctx, http.MethodPatch, embeddingPipelinesPath(serviceID)+"/"+pipelineID, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var p EmbeddingPipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateEmbeddingPipeline response: %w", err)
	}
	return &p, nil
}

// DeleteEmbeddingPipeline deletes an embedding pipeline. When removeData is
// true the companion vector table is dropped as well; otherwise the embedded
// vectors are left in place.
func (c *Client) DeleteEmbeddingPipeline(ctx context.Context, serviceID, pipelineID string, removeData bool) error {
	path := embeddingPipelinesPath(serviceID) + "/" + pipelineID
	if removeData {
		path += "?remove_data=true"
	}
	resp, err := c.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// PauseEmbeddingPipeline pauses an active embedding pipeline.
func (c *Client) PauseEmbeddingPipeline(ctx context.Context, serviceID, pipelineID string) error {
	resp, err := c.do(ctx, http.MethodPost, embeddingPipelinesPath(serviceID)+"/"+pipelineID+"/pause", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// ResumeEmbeddingPipeline resumes a paused embedding pipeline.
func (c *Client) ResumeEmbeddingPipeline(ctx context.Context, serviceID, pipelineID string) error {
	resp, err := c.do(ctx, http.MethodPost, embeddingPipelinesPath(serviceID)+"/"+pipelineID+"/resume", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// TriggerEmbeddingPipelineRun enqueues one manual run for a scheduled or
// manual pipeline. The run is accepted asynchronously in the queued status;
// poll GetEmbeddingPipelineRun until it finishes. Continuous pipelines have
// no discrete runs and reject this call.
func (c *Client) TriggerEmbeddingPipelineRun(ctx context.Context, serviceID, pipelineID string) (*EmbeddingPipelineRun, error) {
	resp, err := c.do(ctx, http.MethodPost, embeddingPipelinesPath(serviceID)+"/"+pipelineID+"/runs", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var run EmbeddingPipelineRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("foundrydb: decode TriggerEmbeddingPipelineRun response: %w", err)
	}
	return &run, nil
}

// ListEmbeddingPipelineRuns returns the latest runs of a pipeline, newest first.
func (c *Client) ListEmbeddingPipelineRuns(ctx context.Context, serviceID, pipelineID string) ([]EmbeddingPipelineRun, error) {
	resp, err := c.do(ctx, http.MethodGet, embeddingPipelinesPath(serviceID)+"/"+pipelineID+"/runs", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listEmbeddingPipelineRunsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListEmbeddingPipelineRuns response: %w", err)
	}
	return result.Runs, nil
}

// GetEmbeddingPipelineRun returns one run of a pipeline.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetEmbeddingPipelineRun(ctx context.Context, serviceID, pipelineID, runID string) (*EmbeddingPipelineRun, error) {
	resp, err := c.do(ctx, http.MethodGet, embeddingPipelinesPath(serviceID)+"/"+pipelineID+"/runs/"+runID, nil, "")
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
	var run EmbeddingPipelineRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetEmbeddingPipelineRun response: %w", err)
	}
	return &run, nil
}
