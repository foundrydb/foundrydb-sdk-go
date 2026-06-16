package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Vector search: typed, strictly read-only similarity search over pgvector
// columns, brokered through the platform's read-only data plane. The
// controller composes the SQL from validated inputs; results are row-capped.

// VectorSearchMetric selects the pgvector distance operator.
type VectorSearchMetric string

const (
	// VectorMetricCosine uses cosine distance (default).
	VectorMetricCosine VectorSearchMetric = "cosine"
	// VectorMetricL2 uses euclidean distance.
	VectorMetricL2 VectorSearchMetric = "l2"
	// VectorMetricInnerProduct uses negative inner product.
	VectorMetricInnerProduct VectorSearchMetric = "ip"
)

// VectorSearchFilter is one column filter applied to the search. Only the
// "eq" operator is supported; Value must be a string, number, or boolean.
type VectorSearchFilter struct {
	Column string      `json:"column"`
	Op     string      `json:"op"`
	Value  interface{} `json:"value"`
}

// VectorSearchRequest is the body for VectorSearch. Exactly one of Vector or
// QueryText must be set; QueryText additionally requires PipelineID so the
// platform embeds the text with the same provider, model, and dimensions
// that produced the indexed vectors.
type VectorSearchRequest struct {
	DatabaseName    string               `json:"database_name"`
	Schema          string               `json:"schema,omitempty"`
	Table           string               `json:"table"`
	EmbeddingColumn string               `json:"embedding_column,omitempty"`
	Vector          []float32            `json:"vector,omitempty"`
	QueryText       string               `json:"query_text,omitempty"`
	PipelineID      *string              `json:"pipeline_id,omitempty"`
	TopK            int                  `json:"top_k,omitempty"`
	Metric          VectorSearchMetric   `json:"metric,omitempty"`
	Filters         []VectorSearchFilter `json:"filters,omitempty"`
	IncludeColumns  []string             `json:"include_columns,omitempty"`
}

// VectorSearchColumn describes one result column.
type VectorSearchColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// VectorSearchResponse is the result of a vector search, with the search
// parameters echoed back.
type VectorSearchResponse struct {
	Columns     []VectorSearchColumn `json:"columns"`
	Rows        [][]interface{}      `json:"rows"`
	RowCount    int                  `json:"row_count"`
	Truncated   bool                 `json:"truncated"`
	ExecutionMs int64                `json:"execution_ms"`
	Metric      VectorSearchMetric   `json:"metric"`
	TopK        int                  `json:"top_k"`
}

// VectorSearch runs a read-only pgvector similarity search against a managed
// PostgreSQL service and returns the matching rows synchronously.
func (c *Client) VectorSearch(ctx context.Context, serviceID string, req VectorSearchRequest) (*VectorSearchResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/managed-services/"+serviceID+"/vector-search", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result VectorSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode VectorSearch response: %w", err)
	}
	return &result, nil
}
