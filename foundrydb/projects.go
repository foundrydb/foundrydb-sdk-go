package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProjectResourceKind is the family of a declared resource inside a project.
// Each kind maps to an existing platform path: postgres and files create a
// managed service, app creates an app-kind service, and auth enables an
// auth configuration on the referenced app.
type ProjectResourceKind string

const (
	ProjectResourceKindPostgres ProjectResourceKind = "postgres"
	ProjectResourceKindFiles    ProjectResourceKind = "files"
	ProjectResourceKindAuth     ProjectResourceKind = "auth"
	ProjectResourceKindApp      ProjectResourceKind = "app"
)

// ProjectStatus is the aggregate lifecycle of a project. The flow is
// Pending -> Provisioning -> Wiring -> Running, with RollingBack -> Failed on
// a terminal child failure during a first deploy, and Deleting -> Deleted on
// an explicit teardown.
type ProjectStatus string

const (
	ProjectStatusPending      ProjectStatus = "Pending"
	ProjectStatusProvisioning ProjectStatus = "Provisioning"
	ProjectStatusWiring       ProjectStatus = "Wiring"
	ProjectStatusRunning      ProjectStatus = "Running"
	ProjectStatusRollingBack  ProjectStatus = "RollingBack"
	ProjectStatusFailed       ProjectStatus = "Failed"
	ProjectStatusDeleting     ProjectStatus = "Deleting"
	ProjectStatusDeleted      ProjectStatus = "Deleted"
)

// ProjectResourceStatus is the lifecycle of a single declared resource.
type ProjectResourceStatus string

const (
	ProjectResourceStatusPending      ProjectResourceStatus = "Pending"
	ProjectResourceStatusProvisioning ProjectResourceStatus = "Provisioning"
	ProjectResourceStatusRunning      ProjectResourceStatus = "Running"
	ProjectResourceStatusFailed       ProjectResourceStatus = "Failed"
	ProjectResourceStatusDeleting     ProjectResourceStatus = "Deleting"
	ProjectResourceStatusDeleted      ProjectResourceStatus = "Deleted"
)

// ProjectDeploymentStatus is the lifecycle of a single deploy attempt.
type ProjectDeploymentStatus string

const (
	ProjectDeploymentStatusPending    ProjectDeploymentStatus = "Pending"
	ProjectDeploymentStatusInProgress ProjectDeploymentStatus = "InProgress"
	ProjectDeploymentStatusSucceeded  ProjectDeploymentStatus = "Succeeded"
	ProjectDeploymentStatusFailed     ProjectDeploymentStatus = "Failed"
)

// ProjectDeployAction is the planner's decision for one resource on a deploy.
type ProjectDeployAction string

const (
	// ProjectDeployActionCreate provisions a resource that does not yet exist.
	ProjectDeployActionCreate ProjectDeployAction = "create"
	// ProjectDeployActionUpdate re-applies a changed spec to an existing resource.
	ProjectDeployActionUpdate ProjectDeployAction = "update"
	// ProjectDeployActionNoop leaves an unchanged, healthy resource untouched.
	ProjectDeployActionNoop ProjectDeployAction = "noop"
	// ProjectDeployActionUnmanaged flags a previously-deployed resource that is
	// absent from the new descriptor. It is never deleted automatically.
	ProjectDeployActionUnmanaged ProjectDeployAction = "unmanaged"
)

// ProjectPlanItem is the planner's decision for one resource on a deploy.
type ProjectPlanItem struct {
	// LogicalName is the resource's declared name in the descriptor.
	LogicalName string              `json:"logical_name"`
	// Kind is the platform primitive this resource represents.
	Kind        ProjectResourceKind `json:"kind"`
	// Action is what the deploy engine will do for this resource.
	Action      ProjectDeployAction `json:"action"`
	// Detail is an optional human-facing note about the planned action.
	Detail      string              `json:"detail,omitempty"`
}

// ProjectDescriptorResource is one resource entry in a descriptor. Spec is an
// untyped map interpreted per kind against the existing service request DTOs.
type ProjectDescriptorResource struct {
	LogicalName string              `json:"logical_name"`
	Kind        ProjectResourceKind `json:"kind"`
	Spec        map[string]any      `json:"spec"`
}

// ProjectDescriptor is the compiled form of a developer's foundry.config.ts:
// the resources the project declares, their inter-resource wiring, and the
// dependency ordering. The CLI POSTs it to POST /projects/deploy.
type ProjectDescriptor struct {
	// Name is the stable project identity across re-deploys.
	Name           string                      `json:"name"`
	// OrganizationID optionally scopes the project to an organization the
	// requesting user belongs to. When empty the project is personal.
	OrganizationID *string                     `json:"organization_id,omitempty"`
	// Resources lists every declared resource by logical name.
	Resources      []ProjectDescriptorResource `json:"resources"`
	// Dependencies maps a resource logical name to the names that must be
	// Running before it is created and wired.
	Dependencies   map[string][]string         `json:"dependencies,omitempty"`
}

// ProjectResource is one declared resource of a project: a pointer to the real
// child service (or app-attached resource) plus its lifecycle and resolved spec.
type ProjectResource struct {
	ID          string                `json:"id"`
	ProjectID   string                `json:"project_id"`
	LogicalName string                `json:"logical_name"`
	Kind        ProjectResourceKind   `json:"kind"`
	// ServiceID is the child service UUID for service-backed kinds
	// (postgres, files, app). Empty until provisioning succeeds.
	ServiceID    string                `json:"service_id,omitempty"`
	// RefID is the app-attached resource id for the auth kind.
	// Empty until provisioning succeeds.
	RefID        string                `json:"ref_id,omitempty"`
	Status       ProjectResourceStatus `json:"status"`
	StatusDetail string                `json:"status_detail"`
	// Provisioned is true once the resource reached Running at least once.
	Provisioned bool     `json:"provisioned"`
	DependsOn   []string `json:"depends_on"`
	Sequence    int      `json:"sequence"`
	Spec        map[string]any `json:"spec,omitempty"`
	// Outputs holds non-secret values downstream resources consume.
	Outputs   map[string]any `json:"outputs,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Project is a developer-owned, re-deployable grouping of declared resources.
// Provisioning is asynchronous: the project is created in Pending and reaches
// Running once all child resources are wired.
type Project struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	OrganizationID string        `json:"organization_id,omitempty"`
	Status         ProjectStatus `json:"status"`
	StatusDetail   string        `json:"status_detail"`
	// LastDeploymentID is the most recent deploy attempt.
	LastDeploymentID string            `json:"last_deployment_id,omitempty"`
	Resources        []ProjectResource `json:"resources,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ProjectDeployment records one deploy attempt and its computed plan.
type ProjectDeployment struct {
	ID            string                  `json:"id"`
	ProjectID     string                  `json:"project_id"`
	Status        ProjectDeploymentStatus `json:"status"`
	IsFirstDeploy bool                    `json:"is_first_deploy"`
	Plan          []ProjectPlanItem       `json:"plan"`
	Error         string                  `json:"error,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

// ProjectDeployRequest is the body of POST /projects/deploy.
type ProjectDeployRequest struct {
	Descriptor ProjectDescriptor `json:"descriptor"`
}

// ProjectDeployResponse is returned by POST /projects/deploy: the deployment
// to poll plus the computed plan so the caller can see what will happen.
type ProjectDeployResponse struct {
	ProjectID    string            `json:"project_id"`
	DeploymentID string            `json:"deployment_id"`
	Status       ProjectStatus     `json:"status"`
	Plan         []ProjectPlanItem `json:"plan"`
}

// projectDeploymentDetailResponse is the response body for
// GET /projects/{name}/deployments/{id}.
type projectDeploymentDetailResponse struct {
	Deployment    ProjectDeployment `json:"deployment"`
	ProjectStatus ProjectStatus     `json:"project_status"`
	StatusDetail  string            `json:"status_detail"`
	Resources     []ProjectResource `json:"resources"`
}

type listProjectsResponse struct {
	Projects []Project `json:"projects"`
}

// DeployProject submits a descriptor to POST /projects/deploy and returns the
// initial deploy response including the computed plan. Provisioning is
// asynchronous: use GetProjectDeployment to poll for the final outcome.
//
// If a project with the given descriptor name already exists, this call is an
// idempotent re-deploy: unchanged resources receive a noop plan action and
// resources absent from the new descriptor are flagged as unmanaged (never
// deleted automatically).
func (c *Client) DeployProject(ctx context.Context, descriptor ProjectDescriptor) (*ProjectDeployResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/projects/deploy", ProjectDeployRequest{Descriptor: descriptor}, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result ProjectDeployResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode DeployProject response: %w", err)
	}
	return &result, nil
}

// ListProjects returns all projects visible to the authenticated user.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	resp, err := c.do(ctx, http.MethodGet, "/projects", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listProjectsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListProjects response: %w", err)
	}
	return result.Projects, nil
}

// GetProject returns the project with the given name, including its child
// resources. Returns nil, nil when it does not exist (404).
func (c *Client) GetProject(ctx context.Context, name string) (*Project, error) {
	resp, err := c.do(ctx, http.MethodGet, "/projects/"+name, nil, "")
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
	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetProject response: %w", err)
	}
	return &project, nil
}

// GetProjectDeployment returns the deployment detail for the given project and
// deployment ID, including the project status, status detail, and resource list.
// Returns nil, nil when the project or deployment does not exist (404).
func (c *Client) GetProjectDeployment(ctx context.Context, projectName, deploymentID string) (*projectDeploymentDetailResponse, error) {
	path := "/projects/" + projectName + "/deployments/" + deploymentID
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
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
	var detail projectDeploymentDetailResponse
	if err := json.Unmarshal(data, &detail); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetProjectDeployment response: %w", err)
	}
	return &detail, nil
}

// DeleteProject initiates teardown of the project. Returns a 202 response body
// with a status field. A 404 is treated as success (idempotent). Resources
// that were never provisioned are removed immediately; provisioned resources
// are torn down via their own service delete paths.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/projects/"+name, nil, "")
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

// WaitForProjectRunning polls the project until it reaches "Running" status or
// the timeout expires. Polling interval is 10 seconds. The context deadline (if
// any) takes precedence over timeout. Returns an error immediately when the
// project enters a terminal failure state.
func (c *Client) WaitForProjectRunning(ctx context.Context, name string, timeout time.Duration) (*Project, error) {
	deadline := time.Now().Add(timeout)
	for {
		project, err := c.GetProject(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("foundrydb: polling project %q: %w", name, err)
		}
		if project == nil {
			return nil, fmt.Errorf("foundrydb: project %q not found while waiting for running status", name)
		}

		status := strings.ToLower(string(project.Status))
		if status == "running" {
			return project, nil
		}
		if strings.Contains(status, "failed") || status == "deleted" {
			return nil, fmt.Errorf("foundrydb: project %q entered terminal status %q", name, project.Status)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("foundrydb: timed out after %s waiting for project %q to reach running status (current: %s)",
				timeout, name, project.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
