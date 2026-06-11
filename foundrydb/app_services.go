package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AppContainerConfig is the container configuration for an app service: the OCI
// image to run, the port it listens on, and its environment. A changed image
// reference or environment triggers a zero-downtime blue/green redeploy.
type AppContainerConfig struct {
	ImageRef      string            `json:"image_ref"`
	ContainerPort int               `json:"container_port"`
	Env           map[string]string `json:"env,omitempty"`
	// CustomDomains are extra hostnames the app is served on beyond
	// {name}.foundrydb.com. Point each at the app (CNAME to the primary domain
	// or A record to the floating IP); the platform issues certificates
	// automatically. Up to 5; foundrydb.com subdomains are not allowed.
	CustomDomains []string `json:"custom_domains,omitempty"`
	// RegistryUsername and RegistryPassword authenticate the image pull from a
	// private registry (provide both, or neither for public images). The
	// registry host is derived from ImageRef. RegistryPassword is write-only:
	// the API never returns it, and on update it is preserved when omitted.
	RegistryUsername string `json:"registry_username,omitempty"`
	RegistryPassword string `json:"registry_password,omitempty"`
	// HealthCheckPath is the HTTP path probed to decide whether a container is
	// healthy during a blue/green redeploy and at runtime (e.g. /healthz). When
	// empty the platform falls back to a TCP connect on ContainerPort.
	HealthCheckPath string `json:"health_check_path,omitempty"`
	// HealthCheckIntervalSeconds is how often the health probe runs.
	HealthCheckIntervalSeconds int `json:"health_check_interval_seconds,omitempty"`
	// HealthCheckTimeoutSeconds is how long a single probe may take before it is
	// counted as a failure.
	HealthCheckTimeoutSeconds int `json:"health_check_timeout_seconds,omitempty"`
	// HealthCheckHealthyThreshold is the number of consecutive successful probes
	// required before a new container is promoted to serve traffic.
	HealthCheckHealthyThreshold int `json:"health_check_healthy_threshold,omitempty"`
}

// AppService is a customer application container hosted on the platform. It runs
// next to the user's managed databases: when attached, the platform peers the
// private networks, opens the database firewall to the app subnet, and injects
// connection credentials as environment variables. It is reachable over HTTPS at
// {name}.foundrydb.com.
type AppService struct {
	ID                 string              `json:"id"`
	UserID             string              `json:"user_id"`
	OrganizationID     string              `json:"organization_id,omitempty"`
	Name               string              `json:"name"`
	ServiceKind        string              `json:"service_kind"`
	Status             string              `json:"status"`
	Zone               string              `json:"zone"`
	PlanName           string              `json:"plan_name"`
	StorageSizeGB      int                 `json:"storage_size_gb"`
	StorageTier        string              `json:"storage_tier,omitempty"`
	AllowedCIDRs       []string            `json:"allowed_cidrs,omitempty"`
	AppConfig          *AppContainerConfig `json:"app_config,omitempty"`
	AttachedServiceIDs []string            `json:"attached_service_ids,omitempty"`
	CreatedAt          string              `json:"created_at"`
	UpdatedAt          string              `json:"updated_at"`
}

// CreateAppServiceRequest is the body for CreateAppService.
type CreateAppServiceRequest struct {
	Name               string             `json:"name"`
	PlanName           string             `json:"plan_name"`
	Zone               string             `json:"zone,omitempty"`
	AppConfig          AppContainerConfig `json:"app_config"`
	StorageSizeGB      int                `json:"storage_size_gb,omitempty"`
	StorageTier        string             `json:"storage_tier,omitempty"`
	AttachedServiceIDs []string           `json:"attached_service_ids,omitempty"`
	OrganizationID     string             `json:"organization_id,omitempty"`
}

// UpdateAppServiceRequest is the body for UpdateAppService. A new image
// reference or environment rolls the container through a zero-downtime
// blue/green redeploy; the container port cannot be changed after creation.
type UpdateAppServiceRequest struct {
	AppConfig AppContainerConfig `json:"app_config"`
}

type listAppServicesResponse struct {
	AppServices []AppService `json:"app_services"`
}

// ListAppServices returns all app services visible to the authenticated user.
func (c *Client) ListAppServices(ctx context.Context) ([]AppService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listAppServicesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppServices response: %w", err)
	}
	return result.AppServices, nil
}

// GetAppService returns the app service with the given UUID.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetAppService(ctx context.Context, id string) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+id, nil, "")
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
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppService response: %w", err)
	}
	return &app, nil
}

// CreateAppService deploys a new app container and returns its initial state.
// The service is created in the Pending status; use WaitForAppRunning to block
// until the container is live and reachable over HTTPS.
func (c *Client) CreateAppService(ctx context.Context, req CreateAppServiceRequest) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateAppService response: %w", err)
	}
	return &app, nil
}

// UpdateAppService applies a new container configuration and returns the updated
// state. A changed image or environment triggers an asynchronous zero-downtime
// blue/green redeploy; poll WaitForAppRunning until it returns to running.
func (c *Client) UpdateAppService(ctx context.Context, id string, req UpdateAppServiceRequest) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPatch, "/app-services/"+id, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateAppService response: %w", err)
	}
	return &app, nil
}

// attachDatabaseRequest is the body for AttachDatabase.
type attachDatabaseRequest struct {
	AttachedServiceID string `json:"attached_service_id"`
}

// AttachDatabase attaches a managed database to a running app and returns the
// updated app service. The platform peers the private networks, opens the
// database firewall to the app's subnet, and rolls a zero-downtime redeploy so
// the new connection credentials are injected; the app passes through
// PendingModification before returning to running. An app supports up to five
// attached databases. The database must be Running, owned by the same user, and
// in the app's peering region. Poll WaitForAppRunning until it returns to
// running.
func (c *Client) AttachDatabase(ctx context.Context, appServiceID, attachedServiceID string) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/attachments", attachDatabaseRequest{AttachedServiceID: attachedServiceID}, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode AttachDatabase response: %w", err)
	}
	return &app, nil
}

// DetachDatabase removes a database attachment from a running app and returns
// the updated app service. The platform reverts the database firewall opening,
// tears down the peering, and rolls a zero-downtime redeploy so the connection
// credentials are removed; the app passes through PendingModification before
// returning to running. Poll WaitForAppRunning until it returns to running.
func (c *Client) DetachDatabase(ctx context.Context, appServiceID, attachmentID string) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodDelete, "/app-services/"+appServiceID+"/attachments/"+attachmentID, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode DetachDatabase response: %w", err)
	}
	return &app, nil
}

// AppDeployment is a single revision in an app service's deploy history: the
// image and configuration that were rolled out at CreatedAt. The newest entry
// reflects the currently serving container; pass an older entry's ID to
// RollbackAppService to redeploy it.
type AppDeployment struct {
	ID               string            `json:"id"`
	ServiceID        string            `json:"service_id"`
	ImageRef         string            `json:"image_ref"`
	ContainerPort    int               `json:"container_port"`
	Env              map[string]string `json:"env,omitempty"`
	CustomDomains    []string          `json:"custom_domains,omitempty"`
	RegistryUsername string            `json:"registry_username,omitempty"`
	// Reason describes what triggered this deployment (e.g. a config update or a
	// rollback).
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// scaleAppServiceRequest is the body for ScaleAppService.
type scaleAppServiceRequest struct {
	PlanName string `json:"plan_name"`
}

// ScaleAppService changes the compute tier of an app service and returns the
// updated state. Scaling up is a zero-downtime hot resize of the running VM;
// scaling down may require a brief restart. The operation is asynchronous; poll
// WaitForAppRunning until it returns to running.
func (c *Client) ScaleAppService(ctx context.Context, appServiceID, planName string) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/scale", scaleAppServiceRequest{PlanName: planName}, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ScaleAppService response: %w", err)
	}
	return &app, nil
}

type listAppDeploymentsResponse struct {
	Deployments []AppDeployment `json:"deployments"`
}

// ListAppDeployments returns the deploy history of an app service, newest
// first. Each entry is a previously rolled-out image and configuration; pass an
// entry's ID to RollbackAppService to redeploy it.
func (c *Client) ListAppDeployments(ctx context.Context, appServiceID string) ([]AppDeployment, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/deployments", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listAppDeploymentsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppDeployments response: %w", err)
	}
	return result.Deployments, nil
}

// rollbackAppServiceRequest is the body for RollbackAppService.
type rollbackAppServiceRequest struct {
	DeploymentID string `json:"deployment_id"`
}

// RollbackAppService redeploys an earlier deployment (from ListAppDeployments)
// via a zero-downtime blue/green swap and returns the updated state. The
// operation is asynchronous; poll WaitForAppRunning until it returns to
// running.
func (c *Client) RollbackAppService(ctx context.Context, appServiceID, deploymentID string) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/rollback", rollbackAppServiceRequest{DeploymentID: deploymentID}, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RollbackAppService response: %w", err)
	}
	return &app, nil
}

// RestartAppService restarts the app's running container in place.
func (c *Client) RestartAppService(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+id+"/restart", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// DeleteAppService initiates deletion of the app service. The platform reverts
// the attached databases' firewall, tears down the network peerings, and
// destroys the VM. A 404 response is treated as success (idempotent).
func (c *Client) DeleteAppService(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/app-services/"+id, nil, "")
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

// WaitForAppRunning polls the app service until it reaches "Running" status or
// the timeout expires. Polling interval is 10 seconds. The context deadline (if
// any) takes precedence over timeout. Returns an error immediately when the
// service enters a terminal failure state.
func (c *Client) WaitForAppRunning(ctx context.Context, id string, timeout time.Duration) (*AppService, error) {
	deadline := time.Now().Add(timeout)
	for {
		app, err := c.GetAppService(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("foundrydb: polling app service %s: %w", id, err)
		}
		if app == nil {
			return nil, fmt.Errorf("foundrydb: app service %s not found while waiting for running status", id)
		}

		status := strings.ToLower(app.Status)
		if status == "running" {
			return app, nil
		}
		if strings.Contains(status, "failed") || status == "error" {
			return nil, fmt.Errorf("foundrydb: app service %s entered terminal status %q", id, app.Status)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("foundrydb: timed out after %s waiting for app service %s to reach running status (current: %s)",
				timeout, id, app.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
