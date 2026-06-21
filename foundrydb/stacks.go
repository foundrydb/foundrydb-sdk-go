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

// ---------------------------------------------------------------------------
// Phase 2: marketplace template management
// ---------------------------------------------------------------------------

// StackTemplateVisibility controls who can discover and launch a customer-authored template.
type StackTemplateVisibility string

const (
	// StackTemplateVisibilityPrivate makes the template visible only to the creating user.
	StackTemplateVisibilityPrivate StackTemplateVisibility = "private"
	// StackTemplateVisibilityOrgShared makes the template visible to all members of the owning org.
	StackTemplateVisibilityOrgShared StackTemplateVisibility = "org_shared"
	// StackTemplateVisibilityPublic publishes the template to the marketplace (pending review).
	StackTemplateVisibilityPublic StackTemplateVisibility = "public"
)

// CustomerStackTemplate is a customer-authored stack template stored in the platform catalog.
// First-party templates returned by ListStackTemplates use StackTemplateSummary; this type is
// for the writable, customer-owned variants managed via the Phase 2 marketplace API.
type CustomerStackTemplate struct {
	// ID is the server-assigned UUID for this template.
	ID string `json:"id"`
	// Name is the slug identifier for the template (URL-safe, unique within the owning org).
	Name string `json:"name"`
	// DisplayName is the human-readable title shown in the marketplace.
	DisplayName string `json:"display_name"`
	// Description is an optional long-form description shown on the template detail page.
	Description string `json:"description"`
	// Version is a caller-supplied semantic version string (e.g. "1.0.0").
	Version string `json:"version"`
	// Visibility controls who can discover and launch this template.
	Visibility StackTemplateVisibility `json:"visibility"`
	// Published marks whether the template is live in the marketplace. Only
	// templates with Visibility=public and Published=true appear in the catalog.
	Published bool `json:"published"`
	// OrganizationID is the owning organization UUID.
	OrganizationID string `json:"organization_id,omitempty"`
	// Descriptor holds the raw YAML or JSON content of the stack descriptor
	// as submitted via CreateStackTemplate. On read it is returned as-is.
	Descriptor string `json:"descriptor,omitempty"`
	// CostPreview is an optional server-computed cost estimate included when
	// the server can evaluate the descriptor at list/get time.
	CostPreview *StackCostPreview `json:"cost_preview,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// CreateStackTemplateRequest is the body for CreateStackTemplate (POST /stacks/templates).
type CreateStackTemplateRequest struct {
	// Name is a URL-safe slug for the template (required).
	Name string `json:"name"`
	// DisplayName is the human-readable title (required).
	DisplayName string `json:"display_name"`
	// Description is an optional long-form description.
	Description string `json:"description,omitempty"`
	// Version is a caller-supplied semver string (required, e.g. "1.0.0").
	Version string `json:"version"`
	// Visibility controls who can discover the template (default: private).
	Visibility StackTemplateVisibility `json:"visibility,omitempty"`
	// Descriptor is the raw YAML or JSON content of the stack descriptor (required).
	Descriptor string `json:"descriptor"`
}

// StackUpgradeChangeItem describes one change that will be applied during an upgrade.
type StackUpgradeChangeItem struct {
	// Resource is the symbolic name of the resource being changed.
	Resource string `json:"resource"`
	// Field is the name of the field being changed (e.g. "plan_name", "version").
	Field string `json:"field"`
	// OldValue is the current value of the field (as a string representation).
	OldValue string `json:"old_value"`
	// NewValue is the value that will be set after the upgrade.
	NewValue string `json:"new_value"`
}

// StackUpgradePreview is the result of PreviewStackUpgrade.
type StackUpgradePreview struct {
	// StackID is the stack this preview applies to.
	StackID string `json:"stack_id"`
	// TemplateName is the template that will be used for the upgrade.
	TemplateName string `json:"template_name"`
	// CurrentTemplateVersion is the version currently running on the stack.
	CurrentTemplateVersion string `json:"current_template_version"`
	// NewTemplateVersion is the version the stack will be upgraded to.
	NewTemplateVersion string `json:"new_template_version"`
	// Changes is the list of per-resource field changes this upgrade will apply.
	Changes []StackUpgradeChangeItem `json:"changes"`
	// CurrentMonthlyCost is the current estimated monthly cost for the stack.
	CurrentMonthlyCost float64 `json:"current_monthly_cost"`
	// NewMonthlyCost is the estimated monthly cost after the upgrade.
	NewMonthlyCost float64 `json:"new_monthly_cost"`
	// CostDelta is NewMonthlyCost minus CurrentMonthlyCost (negative = cheaper).
	CostDelta float64 `json:"cost_delta"`
	// Currency is the ISO 4217 code for all cost amounts.
	Currency string `json:"currency"`
}

// ApplyStackUpgradeRequest is the body for ApplyStackUpgrade.
type ApplyStackUpgradeRequest struct {
	// AcceptedMonthlyCost is the new monthly cost previewed by PreviewStackUpgrade.
	// Required; the upgrade is rejected if the freshly computed cost differs by more than $0.01.
	AcceptedMonthlyCost float64 `json:"accepted_monthly_cost"`
}

type listCustomerTemplatesResponse struct {
	Templates []CustomerStackTemplate `json:"templates"`
}

// CreateStackTemplate creates a new customer-authored stack template in the platform catalog.
// The descriptor field must contain the raw YAML or JSON text of the stack descriptor.
// The server validates the descriptor and returns an error when it is malformed.
func (c *Client) CreateStackTemplate(ctx context.Context, req CreateStackTemplateRequest) (*CustomerStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/templates", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var t CustomerStackTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateStackTemplate response: %w", err)
	}
	return &t, nil
}

// ListMyStackTemplates returns all customer-authored stack templates owned by the authenticated user
// (or the active organization when OrgID is set). First-party catalog entries are not included.
func (c *Client) ListMyStackTemplates(ctx context.Context) ([]CustomerStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/templates/mine", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listCustomerTemplatesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListMyStackTemplates response: %w", err)
	}
	return result.Templates, nil
}

// ListMarketplaceStackTemplates returns all published marketplace templates (visibility=public,
// published=true). Both first-party and customer-authored templates that have been published
// appear in this listing.
func (c *Client) ListMarketplaceStackTemplates(ctx context.Context) ([]CustomerStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/templates/marketplace", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listCustomerTemplatesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListMarketplaceStackTemplates response: %w", err)
	}
	return result.Templates, nil
}

// GetCustomerStackTemplate returns the customer-authored template with the given ID.
// Returns nil, nil when not found (404).
func (c *Client) GetCustomerStackTemplate(ctx context.Context, id string) (*CustomerStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/templates/"+id, nil, "")
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
	var t CustomerStackTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetCustomerStackTemplate response: %w", err)
	}
	return &t, nil
}

// DeleteCustomerStackTemplate deletes the customer-authored template with the given ID.
// A 404 response is treated as success (idempotent).
func (c *Client) DeleteCustomerStackTemplate(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/stacks/templates/"+id, nil, "")
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

// PublishStackTemplate marks a customer-authored template as published so it appears in the
// marketplace. The template must have Visibility=public; the server returns 409 otherwise.
func (c *Client) PublishStackTemplate(ctx context.Context, id string) (*CustomerStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/templates/"+id+"/publish", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var t CustomerStackTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PublishStackTemplate response: %w", err)
	}
	return &t, nil
}

// UnpublishStackTemplate removes a customer-authored template from the marketplace without
// deleting it. Existing stacks launched from the template are not affected.
func (c *Client) UnpublishStackTemplate(ctx context.Context, id string) (*CustomerStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/templates/"+id+"/unpublish", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var t CustomerStackTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UnpublishStackTemplate response: %w", err)
	}
	return &t, nil
}

// PreviewStackUpgrade returns the list of changes and cost delta that would result from
// upgrading the given stack to the latest version of its template.
func (c *Client) PreviewStackUpgrade(ctx context.Context, stackID string) (*StackUpgradePreview, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/"+stackID+"/upgrade/preview", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var preview StackUpgradePreview
	if err := json.Unmarshal(data, &preview); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PreviewStackUpgrade response: %w", err)
	}
	return &preview, nil
}

// ApplyStackUpgrade applies the pending upgrade for the given stack. The AcceptedMonthlyCost
// must match the value from a prior PreviewStackUpgrade call within $0.01.
func (c *Client) ApplyStackUpgrade(ctx context.Context, stackID string, req ApplyStackUpgradeRequest) (*Stack, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/"+stackID+"/upgrade", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var stack Stack
	if err := json.Unmarshal(data, &stack); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ApplyStackUpgrade response: %w", err)
	}
	return &stack, nil
}
