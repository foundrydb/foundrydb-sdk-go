package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// TimescaleDB TSL management for PostgreSQL managed services that have the
// timescaledb extension enabled: hypertables, continuous aggregates, and
// retention + compression policies. Every mutating or list call returns a
// task ID; poll the result with GetTimescaleOperation.

// TimescaleDatabaseRequest is the optional body shared by the catalog list
// calls; only the target database is configurable (defaults to defaultdb).
type TimescaleDatabaseRequest struct {
	Database string `json:"database,omitempty"`
}

// CreateHypertableRequest is the body for CreateHypertable.
type CreateHypertableRequest struct {
	Schema            string `json:"schema,omitempty"`
	Table             string `json:"table"`
	TimeColumn        string `json:"time_column"`
	ChunkTimeInterval string `json:"chunk_time_interval,omitempty"`
	Database          string `json:"database,omitempty"`
}

// CreateContinuousAggregateRequest is the body for CreateContinuousAggregate.
type CreateContinuousAggregateRequest struct {
	Schema   string `json:"schema,omitempty"`
	ViewName string `json:"view_name"`
	Query    string `json:"query"`
	WithData bool   `json:"with_data,omitempty"`
	Database string `json:"database,omitempty"`
}

// ContinuousAggregatePolicyRequest is the body for AddContinuousAggregatePolicy.
type ContinuousAggregatePolicyRequest struct {
	Schema           string `json:"schema,omitempty"`
	ViewName         string `json:"view_name"`
	StartOffset      string `json:"start_offset,omitempty"`
	EndOffset        string `json:"end_offset,omitempty"`
	ScheduleInterval string `json:"schedule_interval"`
	Database         string `json:"database,omitempty"`
}

// RetentionPolicyRequest is the body for AddRetentionPolicy.
type RetentionPolicyRequest struct {
	Schema     string `json:"schema,omitempty"`
	Hypertable string `json:"hypertable"`
	DropAfter  string `json:"drop_after"`
	Database   string `json:"database,omitempty"`
}

// TimescaleOrderByColumn is one element of a compression order-by list.
type TimescaleOrderByColumn struct {
	Column     string `json:"column"`
	Desc       bool   `json:"desc,omitempty"`
	NullsFirst *bool  `json:"nulls_first,omitempty"`
}

// EnableCompressionRequest is the body for EnableCompression.
type EnableCompressionRequest struct {
	Schema     string                   `json:"schema,omitempty"`
	Hypertable string                   `json:"hypertable"`
	SegmentBy  []string                 `json:"segment_by,omitempty"`
	OrderBy    []TimescaleOrderByColumn `json:"order_by,omitempty"`
	Database   string                   `json:"database,omitempty"`
}

// CompressionPolicyRequest is the body for AddCompressionPolicy.
type CompressionPolicyRequest struct {
	Schema        string `json:"schema,omitempty"`
	Hypertable    string `json:"hypertable"`
	CompressAfter string `json:"compress_after"`
	Database      string `json:"database,omitempty"`
}

// TimescaleDBAdminResult is the task result for a completed TimescaleDB
// management operation. For list operations Columns/Rows/RowCount hold the
// catalog projection; for mutating operations Message summarises the
// executed statements.
type TimescaleDBAdminResult struct {
	Operation string     `json:"operation"`
	Columns   []string   `json:"columns,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	RowCount  int        `json:"row_count"`
	Message   string     `json:"message,omitempty"`
}

// TimescaleOperationResult is the poll response for a TimescaleDB management
// task. Status mirrors the agent task lifecycle (pending, in_progress,
// completed, failed); Result is set once completed.
type TimescaleOperationResult struct {
	TaskID       string                  `json:"task_id"`
	Status       string                  `json:"status"`
	Result       *TimescaleDBAdminResult `json:"result,omitempty"`
	ErrorMessage string                  `json:"error_message,omitempty"`
}

type timescaleTaskResponse struct {
	TaskID string `json:"task_id"`
}

// timescaleDispatch performs a dispatch call (POST or DELETE) that returns a
// task ID to poll with GetTimescaleOperation.
func (c *Client) timescaleDispatch(ctx context.Context, method, path string, body interface{}) (string, error) {
	resp, err := c.do(ctx, method, path, body, "")
	if err != nil {
		return "", err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return "", err
	}
	var result timescaleTaskResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("foundrydb: decode TimescaleDB dispatch response: %w", err)
	}
	if result.TaskID == "" {
		return "", fmt.Errorf("foundrydb: TimescaleDB dispatch response missing task_id")
	}
	return result.TaskID, nil
}

// CreateHypertable converts an existing table into a hypertable and returns
// the task ID to poll with GetTimescaleOperation.
func (c *Client) CreateHypertable(ctx context.Context, serviceID string, req CreateHypertableRequest) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/hypertables"
	return c.timescaleDispatch(ctx, http.MethodPost, path, req)
}

// ListHypertables requests the hypertable catalog for a database (defaults to
// defaultdb) and returns the task ID to poll with GetTimescaleOperation.
func (c *Client) ListHypertables(ctx context.Context, serviceID, database string) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/hypertables/list"
	return c.timescaleDispatch(ctx, http.MethodPost, path, TimescaleDatabaseRequest{Database: database})
}

// CreateContinuousAggregate creates a continuous aggregate and returns the
// task ID to poll with GetTimescaleOperation.
func (c *Client) CreateContinuousAggregate(ctx context.Context, serviceID string, req CreateContinuousAggregateRequest) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/continuous-aggregates"
	return c.timescaleDispatch(ctx, http.MethodPost, path, req)
}

// ListContinuousAggregates requests the continuous-aggregate catalog for a
// database (defaults to defaultdb) and returns the task ID to poll with
// GetTimescaleOperation.
func (c *Client) ListContinuousAggregates(ctx context.Context, serviceID, database string) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/continuous-aggregates/list"
	return c.timescaleDispatch(ctx, http.MethodPost, path, TimescaleDatabaseRequest{Database: database})
}

// DropContinuousAggregate drops a continuous-aggregate view and returns the
// task ID to poll with GetTimescaleOperation. schema and database default to
// public and defaultdb when empty.
func (c *Client) DropContinuousAggregate(ctx context.Context, serviceID, viewName, schema, database string) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/continuous-aggregates/" + url.PathEscape(viewName) + "?" + timescaleQueryParams(schema, database)
	return c.timescaleDispatch(ctx, http.MethodDelete, path, nil)
}

// AddContinuousAggregatePolicy schedules automatic refresh of a continuous
// aggregate and returns the task ID to poll with GetTimescaleOperation.
func (c *Client) AddContinuousAggregatePolicy(ctx context.Context, serviceID string, req ContinuousAggregatePolicyRequest) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/continuous-aggregate-policies"
	return c.timescaleDispatch(ctx, http.MethodPost, path, req)
}

// AddRetentionPolicy schedules automatic chunk dropping on a hypertable and
// returns the task ID to poll with GetTimescaleOperation.
func (c *Client) AddRetentionPolicy(ctx context.Context, serviceID string, req RetentionPolicyRequest) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/retention-policies"
	return c.timescaleDispatch(ctx, http.MethodPost, path, req)
}

// RemoveRetentionPolicy removes a retention policy and returns the task ID to
// poll with GetTimescaleOperation. schema and database default to public and
// defaultdb when empty.
func (c *Client) RemoveRetentionPolicy(ctx context.Context, serviceID, hypertable, schema, database string) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/retention-policies/" + url.PathEscape(hypertable) + "?" + timescaleQueryParams(schema, database)
	return c.timescaleDispatch(ctx, http.MethodDelete, path, nil)
}

// EnableCompression enables columnar compression on a hypertable and returns
// the task ID to poll with GetTimescaleOperation.
func (c *Client) EnableCompression(ctx context.Context, serviceID string, req EnableCompressionRequest) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/compression"
	return c.timescaleDispatch(ctx, http.MethodPost, path, req)
}

// AddCompressionPolicy schedules automatic compression on a hypertable and
// returns the task ID to poll with GetTimescaleOperation.
func (c *Client) AddCompressionPolicy(ctx context.Context, serviceID string, req CompressionPolicyRequest) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/compression-policies"
	return c.timescaleDispatch(ctx, http.MethodPost, path, req)
}

// RemoveCompressionPolicy removes a compression policy and returns the task
// ID to poll with GetTimescaleOperation. schema and database default to
// public and defaultdb when empty.
func (c *Client) RemoveCompressionPolicy(ctx context.Context, serviceID, hypertable, schema, database string) (string, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/compression-policies/" + url.PathEscape(hypertable) + "?" + timescaleQueryParams(schema, database)
	return c.timescaleDispatch(ctx, http.MethodDelete, path, nil)
}

// GetTimescaleOperation polls a TimescaleDB management task created by any of
// the dispatch methods above. While the agent is still working, Status is
// pending or in_progress and Result is nil; once completed, Result holds the
// catalog projection (list operations) or a summary message (write
// operations). A failed task returns Status "failed" with ErrorMessage set,
// not an *APIError, since the poll call itself succeeded.
func (c *Client) GetTimescaleOperation(ctx context.Context, serviceID, operationID string) (*TimescaleOperationResult, error) {
	path := "/managed-services/" + serviceID + "/timescaledb/operations/" + url.PathEscape(operationID)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result TimescaleOperationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetTimescaleOperation response: %w", err)
	}
	return &result, nil
}

// timescaleQueryParams builds the optional schema/database query string shared
// by the delete-by-name TimescaleDB endpoints.
func timescaleQueryParams(schema, database string) string {
	q := url.Values{}
	if schema != "" {
		q.Set("schema", schema)
	}
	if database != "" {
		q.Set("database", database)
	}
	return q.Encode()
}
