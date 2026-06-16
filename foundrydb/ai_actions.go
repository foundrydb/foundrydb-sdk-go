package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// AI Actions: a prioritized feed of the recommendations the platform already
// generates across the caller's services, a copilot that turns a
// natural-language intent into a previewable plan, and a tier-gated executor
// that delegates a chosen action to its existing brokered, audited handler.
//
// Safety tiers (read_only | confirm | typed_confirm) are enforced server-side.
// In v1 the executor accepts confirm-tier actions only; destructive
// (typed_confirm) actions such as delete and restore are intentionally not
// executable through this surface and are routed to their native flow via the
// feed item's action Href.

// AIActionRef tells a client how to act on a feed item: the action type, the
// safety tier that gates execution, the id of the underlying
// recommendation/advisory, and a deep link the client can navigate to.
type AIActionRef struct {
	Type   string `json:"type"`
	Tier   string `json:"tier"`
	Target string `json:"target"`
	Href   string `json:"href"`
}

// AIActionItem is one prioritized entry in the AI actions feed. Kind is index
// or advisory; Severity is critical, warning, or info. ID is prefixed by kind
// (for example index:<uuid>).
type AIActionItem struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Severity    string      `json:"severity"`
	ServiceID   string      `json:"service_id"`
	ServiceName string      `json:"service_name"`
	Title       string      `json:"title"`
	Summary     string      `json:"summary"`
	Action      AIActionRef `json:"action"`
	CreatedAt   string      `json:"created_at"`
}

// AIActionsResponse is the feed envelope. Total is the number of matching
// items before the limit was applied; Truncated is true when the result set
// was capped by the limit.
type AIActionsResponse struct {
	Items     []AIActionItem `json:"items"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
}

// AIActionsListOptions filters the feed. Empty fields fall back to the API
// defaults (no kind/severity filter, all the caller's services, limit 50).
// Kind is "index" or "advisory"; Severity is the minimum of "info",
// "warning", or "critical"; Limit is capped at 200 by the server.
type AIActionsListOptions struct {
	ServiceID string
	Kind      string
	Severity  string
	Limit     int
}

// CopilotPlanRequest asks the copilot to turn a natural-language intent into a
// previewable plan. ServiceID is optional context: when set it must be a
// service the caller can see, otherwise the request is rejected with 400.
type CopilotPlanRequest struct {
	Intent    string `json:"intent"`
	ServiceID string `json:"service_id,omitempty"`
}

// CopilotStep is one proposed tool call in a plan. Tool is the allowlisted
// tool name (use it as the ActionType when executing the step). Tier is set
// server-side from the tool catalog and is never trusted from the model.
type CopilotStep struct {
	Tool      string                 `json:"tool"`
	Args      map[string]interface{} `json:"args,omitempty"`
	Tier      string                 `json:"tier"`
	Preview   string                 `json:"preview"`
	Rationale string                 `json:"rationale,omitempty"`
}

// CopilotPlan is the previewable plan returned for an intent. Unsupported is
// true when the intent cannot be expressed with the allowlisted tools; Note
// carries a short human-readable explanation. The plan executes nothing.
type CopilotPlan struct {
	Summary     string        `json:"summary"`
	Steps       []CopilotStep `json:"steps"`
	Unsupported bool          `json:"unsupported"`
	Note        string        `json:"note,omitempty"`
}

// ExecuteAIActionRequest carries a single chosen action for the platform to
// execute by delegating to the existing brokered, audited handler. Confirm
// must be true for the v1 confirm-tier actions:
//
//   - apply_index_recommendation: Args["recommendation_id"]
//   - dismiss_advisory: Args["advisory_match_id"], Args["reason"] (non-empty)
//   - scale_service: Args["target_plan_name"], or Args["cpu_cores"] and
//     Args["memory_mb"]; optional Args["storage_mb"]
//   - add_replica: Args["node_name"], Args["zone"]; optional
//     Args["cpu_cores"], Args["memory_mb"], Args["storage_mb"]
//
// Unknown or destructive (typed_confirm) action types are rejected; they are
// not executable through this surface in v1.
type ExecuteAIActionRequest struct {
	ActionType string                 `json:"action_type"`
	ServiceID  string                 `json:"service_id"`
	Args       map[string]interface{} `json:"args,omitempty"`
	Confirm    bool                   `json:"confirm"`
}

// ExecuteAIActionResult is the response envelope for an execution attempt.
// When the gate accepts and delegates, the HTTP status is 200 and Status
// reflects the inner handler's outcome (executed for a 2xx, failed
// otherwise), HTTPStatus carries the inner handler's status code, and Detail
// carries its raw response body. When the gate itself rejects the request
// (missing confirmation, unknown action, destructive action, unowned
// service), the endpoint returns the gate's own 4xx and this envelope is not
// produced.
type ExecuteAIActionResult struct {
	ActionType string          `json:"action_type"`
	Status     string          `json:"status"`
	HTTPStatus int             `json:"http_status"`
	Message    string          `json:"message"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// AI action execution result statuses.
const (
	// AIActionStatusExecuted means the delegated handler returned 2xx.
	AIActionStatusExecuted = "executed"
	// AIActionStatusFailed means the delegated handler returned non-2xx.
	AIActionStatusFailed = "failed"
	// AIActionStatusRejected means the gate refused before delegating.
	AIActionStatusRejected = "rejected"
)

// AI action execution revert statuses, describing whether and how a persisted
// execution was rolled back from the Action Center outcome loop.
const (
	// AIActionRevertStatusRequested means a reversible rollback was dispatched
	// (for example a brokered DROP INDEX agent task) and is in flight.
	AIActionRevertStatusRequested = "requested"
	// AIActionRevertStatusDone means the rollback completed synchronously.
	AIActionRevertStatusDone = "done"
	// AIActionRevertStatusFailed means the rollback was attempted but failed.
	AIActionRevertStatusFailed = "failed"
	// AIActionRevertStatusNotReversible means the action cannot be undone via
	// the Action Center (for example a scale or add-replica operation).
	AIActionRevertStatusNotReversible = "not_reversible"
)

// AIActionExecution is the API view of one persisted Action Center execution.
// It captures identifiers and outcome status only; it never carries secrets,
// credentials, or response bodies. Status is executed when the delegated
// handler returned 2xx, failed otherwise. RevertStatus and RevertedAt are set
// only after a rollback (requested | done | failed | not_reversible).
type AIActionExecution struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"`
	ServiceID      string `json:"service_id"`
	ActionType     string `json:"action_type"`
	TargetID       string `json:"target_id,omitempty"`
	Status         string `json:"status"`
	HTTPStatus     int    `json:"http_status"`
	ActorUserID    string `json:"actor_user_id,omitempty"`
	CreatedAt      string `json:"created_at"`
	RevertedAt     string `json:"reverted_at,omitempty"`
	RevertStatus   string `json:"revert_status,omitempty"`
}

// AIActionExecutionListResponse is the outcome-loop history envelope.
// TotalCount is the number of records returned in Executions.
type AIActionExecutionListResponse struct {
	Executions []AIActionExecution `json:"executions"`
	TotalCount int                 `json:"total_count"`
}

// AIActionExecutionsListOptions filters the execution history. Empty fields
// fall back to the API defaults (all the caller's visible services, limit 50).
// ServiceID, when set, must be a service the caller can see; Limit is capped at
// 200 by the server.
type AIActionExecutionsListOptions struct {
	ServiceID string
	Limit     int
}

// AIActionRollbackResult is the response envelope for an accepted rollback.
// RevertStatus is requested when a brokered undo is in flight (index drop) or
// done when the undo completed synchronously (advisory reactivation). TaskID is
// set only when the undo runs through the agent (index rollback).
type AIActionRollbackResult struct {
	ExecutionID  string `json:"execution_id"`
	ActionType   string `json:"action_type"`
	RevertStatus string `json:"revert_status"`
	Message      string `json:"message"`
	TaskID       string `json:"task_id,omitempty"`
}

// AIActionsList returns the prioritized AI actions feed across the caller's
// services. Read-only; requires the services:read scope.
func (c *Client) AIActionsList(ctx context.Context, opts AIActionsListOptions) (*AIActionsResponse, error) {
	q := url.Values{}
	if opts.ServiceID != "" {
		q.Set("service_id", opts.ServiceID)
	}
	if opts.Kind != "" {
		q.Set("kind", opts.Kind)
	}
	if opts.Severity != "" {
		q.Set("severity", opts.Severity)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	path := "/ai/actions"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AIActionsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode AIActionsList response: %w", err)
	}
	return &result, nil
}

// CopilotPlan turns a natural-language intent into a previewable plan. It
// executes nothing; requires the services:read scope. Returns 501 from the
// server when no model provider is configured for the organization.
func (c *Client) CopilotPlan(ctx context.Context, req CopilotPlanRequest) (*CopilotPlan, error) {
	resp, err := c.do(ctx, http.MethodPost, "/ai/copilot/plan", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var plan CopilotPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CopilotPlan response: %w", err)
	}
	return &plan, nil
}

// ExecuteAIAction executes one confirm-tier action by delegating to its
// brokered, audited handler. Requires the services:write scope. Confirm must
// be true; unknown or destructive action types are rejected by the server.
// When the gate accepts and delegates, inspect Status and HTTPStatus on the
// result for the inner handler's real outcome.
func (c *Client) ExecuteAIAction(ctx context.Context, req ExecuteAIActionRequest) (*ExecuteAIActionResult, error) {
	resp, err := c.do(ctx, http.MethodPost, "/ai/actions/execute", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result ExecuteAIActionResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ExecuteAIAction response: %w", err)
	}
	return &result, nil
}

// AIActionExecutionsList returns the outcome-loop execution history visible to
// the caller, newest first. Read-only; requires the services:read scope. When
// ServiceID is set it must be a service the caller can see; Limit is capped at
// 200 by the server.
func (c *Client) AIActionExecutionsList(ctx context.Context, opts AIActionExecutionsListOptions) (*AIActionExecutionListResponse, error) {
	q := url.Values{}
	if opts.ServiceID != "" {
		q.Set("service_id", opts.ServiceID)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	path := "/ai/actions/executions"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AIActionExecutionListResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode AIActionExecutionsList response: %w", err)
	}
	return &result, nil
}

// AIActionExecutionRollback reverses a reversible execution by id. There is no
// request body. Requires the services:write scope. Reversibility is decided by
// the recorded action type: apply_index_recommendation drops the created index
// (revert_status requested), dismiss_advisory reactivates the advisory
// (revert_status done); scale_service and add_replica are not reversible and
// the server returns 422. The server returns 404 when the execution is not
// found or its service is not visible to the caller.
func (c *Client) AIActionExecutionRollback(ctx context.Context, executionID string) (*AIActionRollbackResult, error) {
	path := fmt.Sprintf("/ai/actions/executions/%s/rollback", url.PathEscape(executionID))
	resp, err := c.do(ctx, http.MethodPost, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AIActionRollbackResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode AIActionExecutionRollback response: %w", err)
	}
	return &result, nil
}
