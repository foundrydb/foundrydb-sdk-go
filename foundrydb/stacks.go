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
	// SourceTemplateID is the UUID of the customer marketplace template this
	// stack was launched from. Empty for first-party catalog launches.
	SourceTemplateID string `json:"source_template_id,omitempty"`
	// SourcePublisherOrgID is the organization that published the marketplace
	// template. Empty for first-party catalog launches.
	SourcePublisherOrgID string          `json:"source_publisher_org_id,omitempty"`
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
// Exactly one of TemplateName (first-party catalog) or TemplateID (customer
// marketplace template) must be set.
type StackPreviewRequest struct {
	// TemplateName selects the catalog descriptor (e.g. "rag-chatbot").
	TemplateName string `json:"template_name,omitempty"`
	// TemplateID selects a customer-authored marketplace template. Use this
	// instead of TemplateName when previewing a marketplace template.
	TemplateID string `json:"template_id,omitempty"`
}

// StackLaunchRequest is the body for LaunchStack.
// Exactly one of TemplateName (first-party catalog) or TemplateID (customer
// marketplace template) must be set.
type StackLaunchRequest struct {
	// Name is the customer-given instance name.
	Name string `json:"name"`
	// TemplateName selects the first-party catalog descriptor (e.g. "rag-chatbot").
	TemplateName string `json:"template_name,omitempty"`
	// TemplateID selects a customer-authored marketplace template. The template
	// must be visible to the caller (publicly published, or org_shared/private
	// within the caller's organization). Mutually exclusive with TemplateName.
	TemplateID string `json:"template_id,omitempty"`
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

// StackVisibility controls who can discover and launch a custom template.
type StackVisibility string

const (
	// StackVisibilityPrivate restricts the template to the owning organization.
	StackVisibilityPrivate StackVisibility = "private"
	// StackVisibilityOrgShared shares the template with the owning
	// organization's members. Published immediately, no platform review.
	StackVisibilityOrgShared StackVisibility = "org_shared"
	// StackVisibilityPublic exposes the template to every organization in the
	// marketplace, subject to platform admin approval.
	StackVisibilityPublic StackVisibility = "public"
)

// StackPublicationStatus is the moderation lifecycle of a custom template.
type StackPublicationStatus string

const (
	// PublicationStatusDraft is the initial state: visible only to the owning org.
	PublicationStatusDraft StackPublicationStatus = "draft"
	// PublicationStatusSubmitted means the author requested public listing;
	// awaiting platform admin review.
	PublicationStatusSubmitted StackPublicationStatus = "submitted"
	// PublicationStatusApproved is set by a platform admin; typically followed
	// immediately by published.
	PublicationStatusApproved StackPublicationStatus = "approved"
	// PublicationStatusPublished means the template is live and launchable.
	PublicationStatusPublished StackPublicationStatus = "published"
	// PublicationStatusRejected means the admin declined the public submission.
	PublicationStatusRejected StackPublicationStatus = "rejected"
	// PublicationStatusUnpublished means the template was published and then
	// withdrawn (by the author or by an admin takedown).
	PublicationStatusUnpublished StackPublicationStatus = "unpublished"
)

// StackDescriptorResource is one resource entry in a StackDescriptor.
type StackDescriptorResource struct {
	Name string         `json:"name"`
	Kind string         `json:"kind"`
	Spec map[string]any `json:"spec"`
}

// StackDescriptor is the declarative definition of a stack.
type StackDescriptor struct {
	APIVersion   string                    `json:"apiVersion,omitempty"`
	Name         string                    `json:"name,omitempty"`
	DisplayName  string                    `json:"displayName,omitempty"`
	Description  string                    `json:"description,omitempty"`
	Version      string                    `json:"version,omitempty"`
	Resources    []StackDescriptorResource `json:"resources"`
	Dependencies map[string][]string       `json:"dependencies,omitempty"`
}

// CustomStackTemplate is a customer-authored stack template.
// Published templates are immutable versions; editing one requires creating a
// new version. The first-party embedded catalog is unaffected by these rows.
type CustomStackTemplate struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	DisplayName       string                 `json:"display_name"`
	Description       string                 `json:"description"`
	Version           string                 `json:"version"`
	Descriptor        StackDescriptor        `json:"descriptor"`
	PublisherUserID   string                 `json:"publisher_user_id"`
	OrganizationID    string                 `json:"organization_id"`
	Visibility        StackVisibility        `json:"visibility"`
	PublicationStatus StackPublicationStatus `json:"publication_status"`
	// ApproverUserID is the admin who approved this template; empty until approval.
	ApproverUserID string `json:"approver_user_id,omitempty"`
	// ApprovedAt is the timestamp of approval; zero until approval.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	// ModerationReason carries the note from the last moderation action.
	ModerationReason string    `json:"moderation_reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CustomTemplateRequest is the authoring payload for creating or updating a
// customer stack template. On PATCH all fields are optional; only non-zero
// fields are applied.
type CustomTemplateRequest struct {
	// Name is the unique template identifier (slug-style). Required on POST.
	Name string `json:"name,omitempty"`
	// DisplayName is the human-readable name shown in the marketplace catalog.
	DisplayName string `json:"display_name,omitempty"`
	// Description is a short description of what this template provisions.
	Description string `json:"description,omitempty"`
	// Version is the semantic version of this descriptor (defaults to "1.0.0").
	Version string `json:"version,omitempty"`
	// Visibility controls who can see and launch this template.
	Visibility StackVisibility `json:"visibility,omitempty"`
	// Descriptor is the authored stack definition validated by the platform.
	Descriptor StackDescriptor `json:"descriptor"`
}

// ResourceChangeType classifies one resource's delta in an upgrade plan.
type ResourceChangeType string

const (
	// ResourceChangeUnchanged means the resource spec is identical; no action.
	ResourceChangeUnchanged ResourceChangeType = "unchanged"
	// ResourceChangeInPlace means a safe, non-destructive edit will be applied
	// (app image redeploy, plan resize, inference remint).
	ResourceChangeInPlace ResourceChangeType = "in_place"
	// ResourceChangeBlocked means the change requires recreating a stateful
	// resource, changing a database engine or version, adding/removing a
	// resource, or changing a container port. A fresh stack is required.
	ResourceChangeBlocked ResourceChangeType = "blocked"
)

// ResourceChange is one resource's classified delta in an upgrade plan.
type ResourceChange struct {
	// SymbolicName is the resource's name from the descriptor.
	SymbolicName string `json:"symbolic_name"`
	// Kind is the platform primitive this resource represents.
	Kind string `json:"kind"`
	// Change classifies the delta for this resource.
	Change ResourceChangeType `json:"change"`
	// Action is the executor verb for an in_place change: "redeploy_app",
	// "scale_app", "scale_service", "remint_inference", or "" for unchanged.
	Action string `json:"action,omitempty"`
	// Reason explains a blocked change.
	Reason string `json:"reason,omitempty"`
	// TargetSpec is the resolved spec to apply for an in_place change.
	TargetSpec map[string]any `json:"target_spec,omitempty"`
	// Done marks an applied step so the reconciler is re-entrant.
	Done bool `json:"done,omitempty"`
}

// StackUpgradePlan is the computed diff between a running stack and the target
// template version, plus the new and delta cost.
type StackUpgradePlan struct {
	// FromVersion is the template version currently snapshotted on the stack.
	FromVersion string `json:"from_version"`
	// ToVersion is the template version the stack would be upgraded to.
	ToVersion string `json:"to_version"`
	// Changes is the per-resource classified delta.
	Changes []ResourceChange `json:"changes"`
	// NewMonthlyCost is the estimated monthly cost after the upgrade.
	NewMonthlyCost float64 `json:"new_monthly_cost"`
	// CurrentMonthlyCost is the monthly cost accepted at launch time.
	CurrentMonthlyCost float64 `json:"current_monthly_cost"`
	// CostDelta is the difference (NewMonthlyCost minus CurrentMonthlyCost).
	CostDelta float64 `json:"cost_delta"`
	// Blocked is true when any resource has a blocked change. The upgrade
	// cannot proceed in place; a fresh stack is required.
	Blocked bool `json:"blocked"`
	// BlockedReasons carries a human-readable description of each blocked change.
	BlockedReasons []string `json:"blocked_reasons,omitempty"`
}

// StackUpgradeRequest is the body for ApplyStackUpgrade.
type StackUpgradeRequest struct {
	// AcceptedMonthlyCost is the new monthly cost the customer accepted after
	// calling PreviewStackUpgrade. Required; enforced as a cost gate.
	AcceptedMonthlyCost *float64 `json:"accepted_monthly_cost,omitempty"`
}

// StackMigrationStatus is the lifecycle of an in-place stack upgrade.
type StackMigrationStatus string

const (
	StackMigrationAccepted  StackMigrationStatus = "Accepted"
	StackMigrationApplying  StackMigrationStatus = "Applying"
	StackMigrationCompleted StackMigrationStatus = "Completed"
	StackMigrationFailed    StackMigrationStatus = "Failed"
)

// StackMigration is one in-flight or completed in-place upgrade of a stack.
// Created when ApplyStackUpgrade is accepted. The reconciler drives it from
// Accepted through Applying to Completed, or to Failed if a step fails (without
// tearing the stack down).
type StackMigration struct {
	ID                  string               `json:"id"`
	StackID             string               `json:"stack_id"`
	FromTemplateVersion string               `json:"from_template_version"`
	ToTemplateVersion   string               `json:"to_template_version"`
	Changes             []ResourceChange     `json:"changes"`
	Status              StackMigrationStatus `json:"status"`
	StatusDetail        string               `json:"status_detail,omitempty"`
	AcceptedMonthlyCost float64              `json:"accepted_monthly_cost"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

type listCustomTemplatesResponse struct {
	Templates []CustomStackTemplate `json:"templates"`
}

// CreateStackTemplate creates a new customer-authored template. The template
// starts in draft status and is only visible to the owning organization.
// Set Visibility to org_shared or public and call PublishStackTemplate to
// share it.
func (c *Client) CreateStackTemplate(ctx context.Context, req CustomTemplateRequest) (*CustomStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/templates", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var tmpl CustomStackTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateStackTemplate response: %w", err)
	}
	return &tmpl, nil
}

// ListMyStackTemplates returns all templates owned by the caller's organization,
// regardless of visibility or publication status.
func (c *Client) ListMyStackTemplates(ctx context.Context) ([]CustomStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/templates/mine", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listCustomTemplatesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListMyStackTemplates response: %w", err)
	}
	return result.Templates, nil
}

// ListMarketplaceStackTemplates returns all customer-authored templates that
// are publicly published in the marketplace. Any organization may launch them.
func (c *Client) ListMarketplaceStackTemplates(ctx context.Context) ([]CustomStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/stacks/templates/marketplace", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listCustomTemplatesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListMarketplaceStackTemplates response: %w", err)
	}
	return result.Templates, nil
}

// GetStackTemplate returns the custom template with the given UUID.
// Returns nil, nil when it does not exist or is not visible to the caller (404).
func (c *Client) GetStackTemplate(ctx context.Context, id string) (*CustomStackTemplate, error) {
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
	var tmpl CustomStackTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetStackTemplate response: %w", err)
	}
	return &tmpl, nil
}

// UpdateStackTemplate partially updates a custom template. Only templates in
// draft, rejected, or unpublished status can be edited. Returns an APIError
// with status 409 when the template is in an immutable state.
func (c *Client) UpdateStackTemplate(ctx context.Context, id string, req CustomTemplateRequest) (*CustomStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPatch, "/stacks/templates/"+id, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var tmpl CustomStackTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateStackTemplate response: %w", err)
	}
	return &tmpl, nil
}

// DeleteStackTemplate soft-deletes a custom template. Stacks already launched
// from it continue to run on their own descriptor snapshot.
// A 404 response is treated as success (idempotent).
func (c *Client) DeleteStackTemplate(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/stacks/templates/"+id, nil, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		resp.Body.Close()
		return nil
	}
	_, err = checkResponse(resp)
	return err
}

// PublishStackTemplate initiates publication of a custom template.
// For org_shared visibility: publishes immediately.
// For public visibility: submits to the platform admin moderation queue.
// The template must have visibility set to org_shared or public first.
func (c *Client) PublishStackTemplate(ctx context.Context, id string) (*CustomStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/templates/"+id+"/publish", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var tmpl CustomStackTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PublishStackTemplate response: %w", err)
	}
	return &tmpl, nil
}

// UnpublishStackTemplate withdraws a template from publication. Stops new
// launches from this template; running stacks are unaffected.
func (c *Client) UnpublishStackTemplate(ctx context.Context, id string) (*CustomStackTemplate, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/templates/"+id+"/unpublish", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var tmpl CustomStackTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UnpublishStackTemplate response: %w", err)
	}
	return &tmpl, nil
}

// --- In-place stack upgrades ---

// PreviewStackUpgrade computes the classified diff between the running stack's
// snapshotted descriptor and the current version of its template, plus the new
// and delta cost. Call this before ApplyStackUpgrade.
//
// When the plan is blocked (plan.Blocked == true), the upgrade cannot proceed
// in place and the customer must launch a fresh stack. A blocked plan still
// returns success (no error); inspect the Changes field for reasons.
func (c *Client) PreviewStackUpgrade(ctx context.Context, stackID string) (*StackUpgradePlan, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/"+stackID+"/upgrade/preview", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var plan StackUpgradePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PreviewStackUpgrade response: %w", err)
	}
	return &plan, nil
}

// ApplyStackUpgrade applies a previewed in-place upgrade of the stack.
// The AcceptedMonthlyCost field in req must match the new cost from
// PreviewStackUpgrade within $0.01; a drift returns APIError status 409.
//
// Returns a StackMigration when the upgrade is accepted (202 Accepted).
// Returns nil, nil when the stack is already on the latest version (200 OK
// with status "up_to_date"). Returns an APIError with status 422 when the
// plan is blocked. Returns an APIError with status 409 when an upgrade is
// already in progress.
func (c *Client) ApplyStackUpgrade(ctx context.Context, stackID string, req StackUpgradeRequest) (*StackMigration, error) {
	resp, err := c.do(ctx, http.MethodPost, "/stacks/"+stackID+"/upgrade", req, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		// up_to_date: the stack is already on the latest version.
		resp.Body.Close()
		return nil, nil
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var mig StackMigration
	if err := json.Unmarshal(data, &mig); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ApplyStackUpgrade response: %w", err)
	}
	return &mig, nil
}
