package foundrydb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Managed RAG services: composition rows over a pgvector database, a Files
// source bucket, and an embedding pipeline, queried through one audited call
// that returns a grounded answer with citations.

// RAGService is a managed RAG service definition.
type RAGService struct {
	ID                  string   `json:"id"`
	OrganizationID      string   `json:"organization_id"`
	Name                string   `json:"name"`
	PGServiceID         string   `json:"pg_service_id"`
	FilesServiceID      *string  `json:"files_service_id,omitempty"`
	EmbeddingPipelineID *string  `json:"embedding_pipeline_id,omitempty"`
	RerankerServiceID   *string  `json:"reranker_service_id,omitempty"`
	FilesPrefix         *string  `json:"files_prefix,omitempty"`
	ChunkStrategy       string   `json:"chunk_strategy"`
	HybridDenseWeight   float64  `json:"hybrid_dense_weight"`
	TopK                int      `json:"top_k"`
	RerankTopN          int      `json:"rerank_top_n"`
	Status              string   `json:"status"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

// CreateRAGServiceRequest creates a RAG service. Zero policy values take the
// platform defaults.
type CreateRAGServiceRequest struct {
	OrganizationID      string   `json:"organization_id"`
	Name                string   `json:"name"`
	PGServiceID         string   `json:"pg_service_id"`
	FilesServiceID      *string  `json:"files_service_id,omitempty"`
	EmbeddingPipelineID *string  `json:"embedding_pipeline_id,omitempty"`
	RerankerServiceID   *string  `json:"reranker_service_id,omitempty"`
	FilesPrefix         *string  `json:"files_prefix,omitempty"`
	ChunkStrategy       string   `json:"chunk_strategy,omitempty"`
	HybridDenseWeight   *float64 `json:"hybrid_dense_weight,omitempty"`
	TopK                int      `json:"top_k,omitempty"`
	RerankTopN          int      `json:"rerank_top_n,omitempty"`
}

// RAGCitation is one supporting passage behind an answer.
type RAGCitation struct {
	SourceRowID string  `json:"source_row_id"`
	Score       float64 `json:"score"`
	Text        string  `json:"text,omitempty"`
}

// RAGQueryResponse is the grounded answer with its citations.
type RAGQueryResponse struct {
	Answer    string        `json:"answer"`
	Citations []RAGCitation `json:"citations"`
	Reranked  bool          `json:"reranked"`
}

// CreateRAGService creates a managed RAG service.
func (c *Client) CreateRAGService(ctx context.Context, req CreateRAGServiceRequest) (*RAGService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/rag-services", req, req.OrganizationID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var svc RAGService
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
}

// ListRAGServices lists an organization's RAG services.
func (c *Client) ListRAGServices(ctx context.Context, orgID string) ([]RAGService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/rag-services?organization_id="+url.QueryEscape(orgID), nil, orgID)
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out struct {
		RAGServices []RAGService `json:"rag_services"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.RAGServices, nil
}

// GetRAGService fetches one RAG service.
func (c *Client) GetRAGService(ctx context.Context, id string) (*RAGService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/rag-services/"+url.PathEscape(id), nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var svc RAGService
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
}

// DeleteRAGService removes a RAG service definition. Customer data in the
// backing services is untouched.
func (c *Client) DeleteRAGService(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/rag-services/"+url.PathEscape(id), nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// QueryRAGService asks the RAG service a question and returns the grounded
// answer with citations.
func (c *Client) QueryRAGService(ctx context.Context, id, question string) (*RAGQueryResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/rag-services/"+url.PathEscape(id)+"/query",
		map[string]string{"question": question}, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var out RAGQueryResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
