package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// StackCostLineItem is one component of a stack cost preview.
type StackCostLineItem struct {
	// SymbolicName is the resource symbolic name as declared in the stack
	// descriptor (e.g. "db", "files", "inference", "app").
	SymbolicName string `json:"symbolic_name"`
	// Kind is the platform primitive this line item covers (database, files,
	// inference, or app).
	Kind string `json:"kind"`
	// Description is a human-readable label for this line item.
	Description string `json:"description"`
	// MonthlyCost is the estimated monthly cost in USD for this resource.
	MonthlyCost float64 `json:"monthly_cost"`
	// IsCeiling marks a line that is a maximum charge (for example an inference
	// budget), not a fixed recurring cost. The UI should label it accordingly.
	IsCeiling bool `json:"is_ceiling,omitempty"`
}

// StackCostPreview is the per-month cost estimate for a stack template.
// The estimate is computed fresh on each call; pass MonthlyTotal as
// AcceptedMonthlyCost when launching to satisfy the cost gate.
type StackCostPreview struct {
	// TemplateName is the template this estimate applies to.
	TemplateName string `json:"template_name"`
	// Currency is the ISO 4217 code for all cost amounts (currently "USD").
	Currency string `json:"currency"`
	// MonthlyTotal is the sum of all line items. Pass this value as
	// AcceptedMonthlyCost in the launch request.
	MonthlyTotal float64 `json:"monthly_total"`
	// LineItems is the per-resource breakdown of MonthlyTotal.
	LineItems []StackCostLineItem `json:"line_items"`
	// Warnings carries non-fatal notes about the estimate (for example, that
	// an inference budget is a ceiling, not a guaranteed charge).
	Warnings []string `json:"warnings,omitempty"`
}

// StackResource is one composed resource of a stack. Each resource maps to
// a child service (database, files, app) or an org-scoped resource (inference
// key). Resources are provisioned in Sequence order respecting DependsOn.
type StackResource struct {
	ID           string    `json:"id"`
	StackID      string    `json:"stack_id"`
	SymbolicName string    `json:"symbolic_name"`
	Kind         string    `json:"kind"`
	// ServiceID is the child service UUID for service-backed kinds (database,
	// files, app). Empty until provisioning succeeds.
	ServiceID string `json:"service_id,omitempty"`
	// RefID is the org-scoped resource UUID for non-service kinds (inference
	// key). Empty until provisioning succeeds.
	RefID        string    `json:"ref_id,omitempty"`
	Status       string    `json:"status"`
	StatusDetail string    `json:"status_detail"`
	DependsOn    []string  `json:"depends_on"`
	Sequence     int       `json:"sequence"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Stack is a launched, customer-owned grouping of platform primitives composed
// from a stack template. The Resources slice carries every child resource with
// its own status. Provisioning is asynchronous: the stack is created in Pending
// and reaches Running once all child resources are wired.
type Stack struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	TemplateName         string          `json:"template_name"`
	TemplateVersion      string          `json:"template_version"`
	Status               string          `json:"status"`
	StatusDetail         string          `json:"status_detail"`
	EndpointURL          string          `json:"endpoint_url,omitempty"`
	EstimatedMonthlyCost float64         `json:"estimated_monthly_cost"`
	OrganizationID       string          `json:"organization_id,omitempty"`
	Resources            []StackResource `json:"resources,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// StackTemplateSummary is one entry from the first-party stack catalog.
type StackTemplateSummary struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	CostPreview *StackCostPreview `json:"cost_preview,omitempty"`
}

// StackPreviewRequest is the body for PreviewStackCost.
type StackPreviewRequest struct {
	// TemplateName selects the catalog descriptor (e.g. "rag-chatbot").
	TemplateName string `json:"template_name"`
}

// StackLaunchRequest is the body for LaunchStack.
type StackLaunchRequest struct {
	// Name is the customer-given instance name.
	Name string `json:"name"`
	// TemplateName selects the catalog descriptor (e.g. "rag-chatbot").
	TemplateName string `json:"template_name"`
	// OrganizationID optionally scopes the stack (and its inference key) to an
	// organization the requesting user belongs to. When empty, the caller's
	// primary billing organization is used.
	OrganizationID string `json:"organization_id,omitempty"`
	// AcceptedMonthlyCost is the estimate the customer accepted after calling
	// PreviewStackCost. Required. The launch is rejected if the freshly
	// computed estimate differs from this value by more than $0.01.
	AcceptedMonthlyCost *float64 `json:"accepted_monthly_cost,omitempty"`
	// Overrides optionally adjusts per-resource spec fields by symbolic name
	// (e.g. bump the db plan_name). Unknown keys are rejected.
	Overrides map[string]map[string]any `json:"overrides,omitempty"`
}

type listStackTemplatesResponse struct {
	Templates []StackTemplateSummary `json:"templates"`
}

// ListStackTemplates returns the first-party stack catalog. Each entry
// includes a fresh cost preview reflecting current plan prices.
func (c *Client) ListStackTemplates(ctx context.Context) ([]StackTemplateSummary, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/templates", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listStackTemplatesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListStackTemplates response: %w", err)
	}
	return result.Templates, nil
}

// PreviewStackCost computes and returns the estimated monthly cost for launching
// a stack from the named template. Call this before LaunchStack and pass the
// returned MonthlyTotal as AcceptedMonthlyCost to satisfy the cost gate.
func (c *Client) PreviewStackCost(ctx context.Context, req StackPreviewRequest) (*StackCostPreview, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/preview", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var preview StackCostPreview
	if err := json.Unmarshal(data, &preview); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PreviewStackCost response: %w", err)
	}
	return &preview, nil
}

// LaunchStack provisions a stack from a catalog template and returns its initial
// state. The stack is created in Pending; use WaitForStackRunning to block until
// all child resources are wired and the endpoint is live.
//
// AcceptedMonthlyCost in req must match the estimate from a prior
// PreviewStackCost call within $0.01; a material drift returns an APIError with
// status 409. The owning organization must have an enabled inference provider
// when the template composes inference; a missing provider returns 400.
func (c *Client) LaunchStack(ctx context.Context, req StackLaunchRequest) (*Stack, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var stack Stack
	if err := json.Unmarshal(data, &stack); err != nil {
		return nil, fmt.Errorf("foundrydb: decode LaunchStack response: %w", err)
	}
	return &stack, nil
}

type listStacksResponse struct {
	Stacks []Stack `json:"stacks"`
}

// ListStacks returns all stacks visible to the authenticated user.
func (c *Client) ListStacks(ctx context.Context) ([]Stack, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listStacksResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListStacks response: %w", err)
	}
	return result.Stacks, nil
}

// GetStack returns the stack with the given UUID, including its child
// resources. Returns nil, nil when it does not exist (404).
func (c *Client) GetStack(ctx context.Context, id string) (*Stack, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/"+id, nil, "")
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
	var stack Stack
	if err := json.Unmarshal(data, &stack); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetStack response: %w", err)
	}
	return &stack, nil
}

// DeleteStack initiates atomic teardown of the stack. The reconciler removes
// every child resource before settling on Deleted. A 404 response is treated
// as success (idempotent).
func (c *Client) DeleteStack(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/stacks/"+id, nil, "")
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

// RetryStack resets a Failed stack to Pending and re-runs provisioning from
// the beginning. A failed stack has already rolled back all child resources, so
// retry provisions fresh. Returns an APIError with status 409 when the stack is
// not in the Failed state.
func (c *Client) RetryStack(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/"+id+"/retry", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// WaitForStackRunning polls the stack until it reaches "Running" status or the
// timeout expires. Polling interval is 10 seconds (stack provisioning composes
// multiple child resources and takes several minutes). The context deadline (if
// any) takes precedence over timeout. Returns an error immediately when the
// stack enters a terminal failure state.
func (c *Client) WaitForStackRunning(ctx context.Context, id string, timeout time.Duration) (*Stack, error) {
	deadline := time.Now().Add(timeout)
	for {
		stack, err := c.GetStack(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("foundrydb: polling stack %s: %w", id, err)
		}
		if stack == nil {
			return nil, fmt.Errorf("foundrydb: stack %s not found while waiting for running status", id)
		}

		status := strings.ToLower(stack.Status)
		if status == "running" {
			return stack, nil
		}
		if strings.Contains(status, "failed") || status == "deleted" {
			return nil, fmt.Errorf("foundrydb: stack %s entered terminal status %q", id, stack.Status)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("foundrydb: timed out after %s waiting for stack %s to reach running status (current: %s)",
				timeout, id, stack.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
