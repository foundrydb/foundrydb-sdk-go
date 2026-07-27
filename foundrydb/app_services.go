package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AppSource declares that an app builds its image from source instead of
// deploying a prebuilt one. When Type is "git", the platform clones the
// repository, builds an image on the app's own VM, pushes it to the platform
// registry, and the built digest becomes the config's ImageRef (a build
// output, not a caller input).
//
// Current scope: git is the only usable source type. A repository with a
// Dockerfile is built from it; a repository without one is built with Cloud
// Native Buildpacks. Type "upload" is rejected by the API: there is no source
// upload endpoint, so uploaded source could never be found by the builder.
// IsolationTier "shared" is likewise rejected; "dedicated" (or empty) is the
// only valid tier. There is no push-to-deploy; rebuilds are explicit via
// TriggerAppBuild or UpdateAppBuildSettings.
//
// A build that fails is recoverable: the app parks in a failed state rather
// than becoming unusable, and correcting the source through
// UpdateAppBuildSettings, TriggerAppBuild, or an app config update retries it
// in place.
type AppSource struct {
	// Type is the source ingestion mode: "image" (or empty, the prebuilt-image
	// behaviour) or "git". "upload" is reserved for a future ingest endpoint and
	// is rejected by the API today.
	Type string `json:"type"`
	// RepoURL is the git repository to build (Type "git"), in https:// or
	// scp-like ssh (git@host:path) form. It must not embed credentials: an
	// https://user:token@host URL is rejected, because the URL is recorded on
	// the build row and echoed in build logs and API responses. Authenticate a
	// private repository with the ssh form plus DeployKey.
	RepoURL string `json:"repo_url,omitempty"`
	// Ref is the branch, tag, or commit to build. Empty builds the default
	// branch.
	Ref string `json:"ref,omitempty"`
	// DeployKey is a private SSH deploy key for a private repository. It is
	// write-only: the API never returns it, and on a build-settings update it is
	// preserved when omitted. It requires an ssh RepoURL: pairing it with an
	// https RepoURL is rejected, because git never offers the key on an https
	// clone and the fetch would silently fall back to anonymous.
	DeployKey string `json:"deploy_key,omitempty"`
	// ScanPolicy decides whether vulnerability findings fail a build. Empty
	// records findings without blocking, which is the only default safe to
	// apply to an app that already deploys. A scan that does not complete
	// blocks under either blocking policy.
	ScanPolicy AppScanPolicy `json:"scan_policy,omitempty"`

	// UploadRef references a previously uploaded source tarball (Type
	// "upload"). Reserved and currently unusable: the "upload" source type is
	// rejected, so this field has no valid use yet.
	UploadRef string `json:"upload_ref,omitempty"`
	// AutoDeploy rebuilds and redeploys the app when a verified push webhook
	// reports a commit on the tracked ref (Ref, or the repository's default
	// branch when Ref is empty). Off by default: a linked repository never
	// deploys on its own until this is set. The signing secret is minted
	// separately by the platform and is not settable here.
	AutoDeploy bool `json:"auto_deploy,omitempty"`
	// Builder selects the build strategy: "" (auto-detect, using the
	// repository's Dockerfile when present and Cloud Native Buildpacks
	// otherwise), "dockerfile" (requires a Dockerfile), or "buildpacks" (always
	// builds with Cloud Native Buildpacks, no Dockerfile needed).
	Builder string `json:"builder,omitempty"`
	// DockerfilePath is the Dockerfile location relative to the build context.
	// Empty defaults to "Dockerfile".
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	// BuildContext is the subdirectory within the source treated as the build
	// root. Empty uses the repository root.
	BuildContext string `json:"build_context,omitempty"`
	// BuildEnv is environment available at build time only; it is not injected
	// into the running container. It is size-limited: at most 100 variables,
	// 4096 bytes per value, and 64 KiB across all keys and values combined.
	BuildEnv map[string]string `json:"build_env,omitempty"`
	// IsolationTier is "" or "dedicated" (single-tenant build compute isolated
	// by the hypervisor, the default and the only valid value). "shared" is
	// rejected: the sandboxed shared pool does not exist, so accepting it would
	// report an isolation boundary weaker than the one the build actually ran
	// under.
	IsolationTier string `json:"isolation_tier,omitempty"`
}

// AppContainerConfig is the container configuration for an app service: the OCI
// image to run, the port it listens on, and its environment. A changed image
// reference or environment triggers a zero-downtime blue/green redeploy.
type AppContainerConfig struct {
	// ImageRef is the full OCI image reference to run. Required unless Source
	// builds from source, in which case it is the build output (the built
	// image's platform-registry digest reference) and is not supplied by the
	// caller.
	ImageRef      string            `json:"image_ref,omitempty"`
	ContainerPort int               `json:"container_port"`
	Env           map[string]string `json:"env,omitempty"`
	// Source, when set with a non-image type, makes the platform build the
	// image from a git repository (with its Dockerfile, or Cloud Native
	// Buildpacks when it has none) instead of pulling a prebuilt ImageRef. See
	// AppSource for the current scope notes.
	Source *AppSource `json:"source,omitempty"`
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
	Name     string `json:"name"`
	PlanName string `json:"plan_name"`
	// Zone is the deployment zone. A source-built app additionally requires a zone
	// whose app template carries the build toolchain; other zones are rejected with 400.
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

// attachDatabaseRequest is the body for AttachDatabase. The scope and wiring
// fields apply only to files (object storage) attachments and are omitted for
// database attachments.
type attachDatabaseRequest struct {
	AttachedServiceID string `json:"attached_service_id"`
	Prefix            string `json:"prefix,omitempty"`
	Permission        string `json:"permission,omitempty"`
	WiringIntent      string `json:"wiring_intent,omitempty"`
}

// AttachOptions carries optional scope and wiring for a files (object storage)
// attachment. The fields apply only to files attachments; leave them zero for
// database attachments. Prefix scopes the minted S3 key to an object key
// prefix; Permission is "read_only" or "read_write" (default); WiringIntent is
// "inject_creds" (default), "on_upload_trigger", or "auto_embed".
type AttachOptions struct {
	Prefix       string
	Permission   string
	WiringIntent string
}

// AttachDatabase attaches a managed service to a running app and returns the
// updated app service. The target may be a database or another app (east-west
// app-to-app). The platform peers the private networks, opens the target's port
// to the app's subnet, and rolls a zero-downtime redeploy so the injected
// environment is updated: a database injects connection credentials, an app
// injects MDB_<NAME>_HOST/PORT/URL for plain-HTTP calls over the private SDN. The
// app passes through PendingModification before returning to running. An app
// supports up to five attachments (databases and apps combined). The target must
// be Running, owned by the same user, in the app's peering region, and not the
// app itself. Poll WaitForAppRunning until it returns to running.
func (c *Client) AttachDatabase(ctx context.Context, appServiceID, attachedServiceID string) (*AppService, error) {
	return c.AttachServiceWithOptions(ctx, appServiceID, attachedServiceID, nil)
}

// AttachServiceWithOptions attaches a managed database or files (object
// storage) service to a running app with optional files-attachment scope and
// wiring, returning the updated app service. Pass nil opts for a database
// attachment or a whole-bucket read-write files attachment. The Prefix and
// Permission scope fields and WiringIntent apply only to files attachments and
// are rejected (when non-default) for database attachments. See AttachDatabase
// for the lifecycle; poll WaitForAppRunning until the app returns to running.
func (c *Client) AttachServiceWithOptions(ctx context.Context, appServiceID, attachedServiceID string, opts *AttachOptions) (*AppService, error) {
	req := attachDatabaseRequest{AttachedServiceID: attachedServiceID}
	if opts != nil {
		req.Prefix = opts.Prefix
		req.Permission = opts.Permission
		req.WiringIntent = opts.WiringIntent
	}
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/attachments", req, "")
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
	Reason string `json:"reason,omitempty"`
	// DeployLogs is the ordered list of deploy steps the agent executed for this
	// revision (image start, health probe, ingress cutover, previous-color
	// teardown). It is distinct from the runtime container logs and is empty for
	// revisions deployed before the platform captured deploy steps.
	DeployLogs []AppDeployStep `json:"deploy_logs,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// AppDeployStep is one phase of an app deploy/redeploy, captured on the agent.
// Status is one of "ok", "failed", or "info".
type AppDeployStep struct {
	Step       string    `json:"step"`
	Status     string    `json:"status"`
	Message    string    `json:"message,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms,omitempty"`
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
// via a zero-downtime blue/green swap and returns the updated state.
//
// On a source-built app the rollback puts the recorded image digest back
// exactly as it was and does not trigger a rebuild, so it cannot be undone by
// commits that landed in the repository since. The source configuration is
// preserved, and the next TriggerAppBuild or UpdateAppBuildSettings call
// rebuilds from source as usual.
//
// The operation is asynchronous; poll WaitForAppRunning until it returns to
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

// AppBuild is one recorded source build of an app service: the attempt to turn
// the app's source (a git repository) into an OCI image. On success the built
// image (a platform-registry digest reference) becomes the app's ImageRef and
// the normal blue/green deploy path takes over. Status is one of "pending",
// "building", "succeeded", or "failed". Trigger is one of "manual", "push",
// "config", or "api"; "push" is reserved, there is no push-to-deploy yet.
type AppBuild struct {
	ID        string `json:"id"`
	ServiceID string `json:"service_id"`
	// DeploymentID links to the deployment revision this build's image fed, set
	// once the successful build is deployed. Nil while building or on failure.
	DeploymentID *string `json:"deployment_id,omitempty"`
	Status       string  `json:"status"`
	Trigger      string  `json:"trigger"`
	// SourceType is the source ingestion mode the build used. Always "git" in
	// practice: "upload" is reserved for a future ingest endpoint and is
	// rejected at the API boundary, so no build is recorded with it.
	SourceType string `json:"source_type"`
	RepoURL    string `json:"repo_url,omitempty"`
	// CommitSHA is the commit the build resolved and checked out. Empty until
	// resolved.
	CommitSHA string `json:"commit_sha,omitempty"`
	// ImageRef and ImageDigest are the build output (registry reference and its
	// resolved sha256 digest). Empty until the build succeeds.
	ImageRef    string `json:"image_ref,omitempty"`
	ImageDigest string `json:"image_digest,omitempty"`
	// ImageSizeBytes is the built image's size as the build host's container
	// store reports it, and CacheHit whether the build reused cached layers.
	// Both are zero/false when the build did not record them, which includes
	// every build made before they were captured: not measured is not the same
	// as measured as zero.
	// CacheHit is a pointer so a measured cold cache (false) stays distinct
	// from a build that never measured it (nil).
	ImageSizeBytes int64 `json:"image_size_bytes,omitempty"`
	CacheHit       *bool `json:"cache_hit,omitempty"`
	// ScanStatus is "succeeded", "failed" or "skipped". Check it BEFORE the
	// counts: a build with no findings and a build that was never scanned both
	// have nil counts, so nil counts alone do not mean a clean image. Empty on
	// builds predating scanning.
	ScanStatus string `json:"scan_status,omitempty"`
	// ScanError explains a scan that could not run.
	ScanError string `json:"scan_error,omitempty"`
	// Vuln* are findings by severity. Pointers so "not measured" stays distinct
	// from "measured as zero".
	VulnCritical *int `json:"vuln_critical,omitempty"`
	VulnHigh     *int `json:"vuln_high,omitempty"`
	VulnMedium   *int `json:"vuln_medium,omitempty"`
	VulnLow      *int `json:"vuln_low,omitempty"`
	// BuildLogs is the ordered list of build steps (clone, detect, build,
	// push), written once the build completes. It reuses the deploy-step shape,
	// so a build log reads like a deployment's DeployLogs.
	BuildLogs []AppDeployStep `json:"build_logs,omitempty"`
	// LogsTrimmed reports that retention reclaimed this build's step log and raw
	// output. The build row itself is kept for as long as the app exists (it
	// records what was deployed and is a rollback target), so this distinguishes
	// pruned logs from a build that never produced any.
	LogsTrimmed  bool       `json:"logs_trimmed,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// AppBuildLogs is the response of GetAppBuildLogs: the build's status, its
// ordered step log, and a slice of the builder's raw output. A still-running
// build carries the steps reported so far (empty until the builder reports
// them); a completed build carries the full set.
type AppBuildLogs struct {
	BuildID string          `json:"build_id"`
	Status  string          `json:"status"`
	Steps   []AppDeployStep `json:"steps"`
	// Output is the raw output of the build tool, starting at the offset the
	// request asked for. Following a running build means passing the previous
	// response's NextOffset back as the next request's offset, so each call
	// returns only what the builder produced since the last one.
	Output string `json:"output,omitempty"`
	// NextOffset is where the next read should resume. Counts characters.
	NextOffset int `json:"next_offset"`
	// Truncated reports that the build produced more output than is retained,
	// so the log is cut rather than finished. The build itself was unaffected.
	Truncated bool `json:"truncated,omitempty"`
	// LogsTrimmed reports that retention reclaimed the logs, as opposed to the
	// build never having produced any.
	LogsTrimmed bool `json:"logs_trimmed,omitempty"`
}

type listAppBuildsResponse struct {
	Builds []AppBuild `json:"builds"`
}

// ListAppBuilds returns the source builds of an app service, newest first.
// limit caps the number of builds returned; pass 0 for the server default
// (50). An app that deploys a prebuilt image has no builds and returns an
// empty slice.
func (c *Client) ListAppBuilds(ctx context.Context, appServiceID string, limit int) ([]AppBuild, error) {
	path := "/app-services/" + appServiceID + "/builds"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listAppBuildsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppBuilds response: %w", err)
	}
	return result.Builds, nil
}

// GetAppBuild returns one source build of an app service. Returns nil, nil
// when the build does not exist for this app (404).
func (c *Client) GetAppBuild(ctx context.Context, appServiceID, buildID string) (*AppBuild, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/builds/"+buildID, nil, "")
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
	var build AppBuild
	if err := json.Unmarshal(data, &build); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppBuild response: %w", err)
	}
	return &build, nil
}

// GetAppBuildLogs returns a build's ordered step log together with its status.
// A still-running build returns the steps reported so far (empty until the
// builder reports them); poll until Status is "succeeded" or "failed" for the
// full set. Returns nil, nil when the build does not exist for this app (404).
func (c *Client) GetAppBuildLogs(ctx context.Context, appServiceID, buildID string) (*AppBuildLogs, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/builds/"+buildID+"/logs", nil, "")
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
	var logs AppBuildLogs
	if err := json.Unmarshal(data, &logs); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppBuildLogs response: %w", err)
	}
	return &logs, nil
}

// TriggerAppBuild queues a rebuild of the app's current stored source without
// changing the build settings, followed by a zero-downtime blue/green redeploy
// of the built image. The app must build from source (an app that deploys a
// prebuilt image is rejected) and must be either running or parked after a
// failed build: a build failure is recoverable, and this is the call that
// retries it once the repository is fixed. Rebuilds are always explicit:
// there is no push-to-deploy, so pushing to the repository does not start a
// build by itself. The operation is asynchronous; follow the build with
// ListAppBuilds and poll WaitForAppRunning until the app returns to running.
func (c *Client) TriggerAppBuild(ctx context.Context, appServiceID string) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/builds", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode TriggerAppBuild response: %w", err)
	}
	return &app, nil
}

// UpdateAppBuildSettingsRequest is the body for UpdateAppBuildSettings: the
// settable build configuration for a source-built app. The source ingestion
// mode and any uploaded source reference are fixed at creation and preserved
// by the endpoint. Fields are replaced wholesale (send the full desired
// settings), with one exception: the write-only DeployKey is preserved when
// omitted, exactly like the registry password on a redeploy. See AppSource for
// the current scope notes (git sources; Dockerfile or Cloud Native Buildpacks
// builds; "dedicated" is the only valid isolation tier).
type UpdateAppBuildSettingsRequest struct {
	RepoURL        string            `json:"repo_url,omitempty"`
	Ref            string            `json:"ref,omitempty"`
	Builder        string            `json:"builder,omitempty"`
	DockerfilePath string            `json:"dockerfile_path,omitempty"`
	BuildContext   string            `json:"build_context,omitempty"`
	BuildEnv       map[string]string `json:"build_env,omitempty"`
	DeployKey      string            `json:"deploy_key,omitempty"`
	IsolationTier  string            `json:"isolation_tier,omitempty"`
	// ScanPolicy decides whether vulnerability findings fail a build. Empty
	// records findings without blocking.
	ScanPolicy AppScanPolicy `json:"scan_policy,omitempty"`
}

// UpdateAppBuildSettings updates a source-built app's build configuration and
// queues a rebuild with the new settings, followed by a zero-downtime
// blue/green redeploy of the built image. The app must be running or parked
// after a failed build, so correcting a bad repository, ref, or Dockerfile path
// here is the supported way to recover a failed build in place. The
// operation is asynchronous; follow the build with ListAppBuilds and poll
// WaitForAppRunning until the app returns to running.
func (c *Client) UpdateAppBuildSettings(ctx context.Context, appServiceID string, req UpdateAppBuildSettingsRequest) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPut, "/app-services/"+appServiceID+"/build-settings", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateAppBuildSettings response: %w", err)
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
	_, err := c.DeleteAppServiceWithOptions(ctx, id, nil)
	return err
}

// DeleteAppServiceOptions carries optional behavior for
// DeleteAppServiceWithOptions.
type DeleteAppServiceOptions struct {
	// CascadeCompanions also deletes the companion database(s) provisioned
	// alongside the app in a complete-solution attach, through the same delete
	// orchestration as a normal database delete. A companion another live app
	// still attaches to is refused (409) and nothing is deleted. When false,
	// companions are kept and continue to run (and bill) as normal database
	// services.
	CascadeCompanions bool
}

// DeleteAppServiceResult reports what the delete scheduled. DeletedCompanions
// lists the names of the companion databases whose deletion was cascaded; it is
// empty when no cascade was requested or the app had no companions.
type DeleteAppServiceResult struct {
	Status            string   `json:"status"`
	DeletedCompanions []string `json:"deleted_companions,omitempty"`
}

// DeleteAppServiceWithOptions initiates deletion of the app service, optionally
// cascading to its complete-solution companion databases. The platform reverts
// the attached databases' firewall, tears down the network peerings, and
// destroys the VM. The cascade is requested as ?cascade=companions (the query
// parameter avoids the token "delete", which the request security scanner
// rejects in query strings). A 404 response is treated as success (idempotent)
// and returns a nil result.
func (c *Client) DeleteAppServiceWithOptions(ctx context.Context, id string, opts *DeleteAppServiceOptions) (*DeleteAppServiceResult, error) {
	path := "/app-services/" + id
	if opts != nil && opts.CascadeCompanions {
		path += "?cascade=companions"
	}
	resp, err := c.do(ctx, http.MethodDelete, path, nil, "")
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
	var result DeleteAppServiceResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode DeleteAppService response: %w", err)
	}
	return &result, nil
}

// AuthSMTPConfig carries the customer-supplied SMTP credentials authd uses to
// send magic-link emails. It is write-only at the API boundary: it is accepted
// on enable, stored in the platform secret store, and never returned by any
// response. Customer-supplied SMTP is required; there is no platform relay.
type AuthSMTPConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"from_address"`
	FromName    string `json:"from_name"`
	// InsecureSkipVerify disables STARTTLS certificate verification for the SMTP
	// leg. It defaults to false (verification on) and exists only for test mail
	// catchers that present a self-signed certificate; never set it for a
	// production SMTP relay.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
}

// AuthThemeConfig is the non-PII branding applied to the hosted login pages.
type AuthThemeConfig struct {
	LogoURL     string `json:"logo_url,omitempty"`
	BrandColor  string `json:"brand_color,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	SupportURL  string `json:"support_url,omitempty"`
}

// AuthIDPProvider is the identifier of a supported social-login provider. The
// set is closed: an unknown provider is rejected at enable time.
type AuthIDPProvider = string

// Supported social-login providers for AuthIDPProviderRequest.Provider.
const (
	AuthIDPProviderGoogle AuthIDPProvider = "google"
	AuthIDPProviderGitHub AuthIDPProvider = "github"
)

// AuthIDPProviderRequest enables one social-login provider at enable time. The
// customer registers an OAuth app at the provider, then supplies its ClientID
// and ClientSecret here. ClientSecret is write-only: it is stored in the
// platform secret store and never returned by any response. A listed provider
// must carry both a ClientID and a ClientSecret; a missing secret disables the
// provider and is rejected rather than persisted.
type AuthIDPProviderRequest struct {
	Provider     AuthIDPProvider `json:"provider"`
	ClientID     string          `json:"client_id"`
	ClientSecret string          `json:"client_secret"`
	DisplayName  string          `json:"display_name,omitempty"`
}

// AuthIDPProviderConfig is the stored, non-secret configuration of one
// social-login provider returned on the AuthConfiguration: the provider id, the
// customer-supplied OAuth client id, and an optional display name shown on the
// login button. The client_secret is never returned; it is custodied in the
// platform secret store.
type AuthIDPProviderConfig struct {
	Provider    AuthIDPProvider `json:"provider"`
	ClientID    string          `json:"client_id"`
	DisplayName string          `json:"display_name,omitempty"`
}

// AuthEnableRequest is the body for EnableAppServiceAuth. AttachmentID names one
// of the app's existing PostgreSQL attachments to back the identity store.
// IssuerDomainChoice is "fallback" (an auth-<id>.foundrydb.com subdomain) or
// "custom" and is fixed at enable time. SMTP is mandatory. IDPProviders
// optionally enables social login (Google and GitHub); an empty list enables
// magic-link login only.
type AuthEnableRequest struct {
	AttachmentID       string                   `json:"attachment_id"`
	IssuerDomainChoice string                   `json:"issuer_domain_choice"`
	SMTP               AuthSMTPConfig           `json:"smtp"`
	Theme              AuthThemeConfig          `json:"theme"`
	IDPProviders       []AuthIDPProviderRequest `json:"idp_providers,omitempty"`
}

// AuthIssuerDomain choices for AuthEnableRequest.IssuerDomainChoice. The issuer
// URL is fixed at enable time; changing it later invalidates outstanding
// tokens, so this is a deliberate one-time choice.
const (
	AuthIssuerDomainFallback = "fallback"
	AuthIssuerDomainCustom   = "custom"
)

// AuthConfiguration is one auth enablement record for an app service. The
// identity data itself lives in the customer's own PostgreSQL database; this
// record holds enablement state only. Secret custody locations are never
// serialized.
type AuthConfiguration struct {
	ID                   string          `json:"id"`
	UserID               string          `json:"user_id"`
	OrganizationID       string          `json:"organization_id,omitempty"`
	AppServiceID         string          `json:"app_service_id"`
	DatabaseServiceID    string          `json:"database_service_id"`
	AttachmentID         string          `json:"attachment_id"`
	IssuerURL            string          `json:"issuer_url"`
	FallbackDomain       string          `json:"fallback_domain"`
	CustomDomain         string          `json:"custom_domain,omitempty"`
	Status               string          `json:"status"`
	SchemaVersionApplied string          `json:"schema_version_applied"`
	FailureReason        string          `json:"failure_reason,omitempty"`
	Theme                AuthThemeConfig `json:"theme"`
	// IDPProviders is the set of configured social-login providers (Google,
	// GitHub) without their secrets. Each entry carries the provider id, the
	// customer-supplied client_id, and an optional display name. An empty slice
	// means social login is not configured.
	IDPProviders     []AuthIDPProviderConfig `json:"idp_providers"`
	AuthAppServiceID string                  `json:"auth_app_service_id,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

// AuthSigningKey is the controller-side record of one JWT signing keypair. The
// key material is held in the platform secret store; only the kid, algorithm,
// and lifecycle status are exposed. Status follows the dual-kid rotation
// lifecycle: pending, active, retiring, retired, or revoked.
type AuthSigningKey struct {
	ID                  string     `json:"id"`
	AuthConfigurationID string     `json:"auth_configuration_id"`
	Kid                 string     `json:"kid"`
	Algorithm           string     `json:"algorithm"`
	Status              string     `json:"status"`
	ActivatedAt         *time.Time `json:"activated_at,omitempty"`
	RetiredAt           *time.Time `json:"retired_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// AuthConfigurationWithKeys is the GET /app-services/{id}/auth response: the
// auth configuration and its signing key records.
type AuthConfigurationWithKeys struct {
	Auth        *AuthConfiguration `json:"auth"`
	SigningKeys []AuthSigningKey   `json:"signing_keys"`
}

// rotateAuthKeyResponse wraps the newly minted signing key returned by
// POST /app-services/{id}/auth/rotate-key.
type rotateAuthKeyResponse struct {
	SigningKey *AuthSigningKey `json:"signing_key"`
}

// EnableAppServiceAuth enables end-user authentication for an app service,
// backed by one of its attached PostgreSQL services, and returns the resulting
// auth configuration. The named attachment must reference a PostgreSQL service;
// the platform provisions the identity schema in the customer database and
// stands up the OIDC issuer. The SMTP credentials in the request are stored in
// the secret store and never returned. To offer Sign in with Google or GitHub,
// set req.IDPProviders; each provider's client_secret is stored in the secret
// store and never returned, and the issuer renders the provider buttons on its
// hosted login page.
func (c *Client) EnableAppServiceAuth(ctx context.Context, appServiceID string, req AuthEnableRequest) (*AuthConfiguration, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/auth/enable", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AuthConfigurationWithKeys
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode EnableAppServiceAuth response: %w", err)
	}
	return result.Auth, nil
}

// GetAppServiceAuth returns the auth configuration and signing key records for
// an app service. Returns nil, nil when auth is not enabled (404).
func (c *Client) GetAppServiceAuth(ctx context.Context, appServiceID string) (*AuthConfigurationWithKeys, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/auth", nil, "")
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
	var result AuthConfigurationWithKeys
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppServiceAuth response: %w", err)
	}
	return &result, nil
}

// DisableAppServiceAuth disables auth for an app service. The end-user identity
// data in the customer's database is left untouched; only the platform-managed
// issuer and enablement state are torn down.
func (c *Client) DisableAppServiceAuth(ctx context.Context, appServiceID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/auth/disable", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// RotateAppServiceAuthKey rotates the JWT signing key and returns the newly
// minted key record. Rotation is dual-kid: the new key is published alongside
// the outgoing one so tokens signed by the previous key keep validating until
// it retires.
func (c *Client) RotateAppServiceAuthKey(ctx context.Context, appServiceID string) (*AuthSigningKey, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/auth/rotate-key", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result rotateAuthKeyResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RotateAppServiceAuthKey response: %w", err)
	}
	return result.SigningKey, nil
}

// RevokeAppServiceAuthSession revokes one end-user session by id. The
// revocation is dispatched asynchronously to the backing database's primary VM.
func (c *Client) RevokeAppServiceAuthSession(ctx context.Context, appServiceID, sessionID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/auth/sessions/"+sessionID+"/revoke", nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// DeleteAppServiceAuthUserRequest addresses the end-user to erase. Set exactly
// one of Email or UserID.
type DeleteAppServiceAuthUserRequest struct {
	// Email addresses the end-user by email. Mutually exclusive with UserID.
	Email string `json:"email,omitempty"`
	// UserID addresses the end-user by their auth subject UUID. Mutually
	// exclusive with Email.
	UserID string `json:"user_id,omitempty"`
}

// DeleteAppServiceAuthUser erases one end-user under the GDPR right to erasure
// (Art. 17), addressed by exactly one of Email or UserID. The erasure removes
// the user and their identity data (identities, sessions, refresh tokens, MFA
// enrolments, pending login/oauth tokens) and scrubs the user's audit-log
// rows. It is dispatched asynchronously to the backing database's primary VM;
// the returned task id is for status polling. The email is never persisted or
// logged controller-side.
func (c *Client) DeleteAppServiceAuthUser(ctx context.Context, appServiceID string, req DeleteAppServiceAuthUserRequest) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/auth/users/delete", req, "")
	if err != nil {
		return "", err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return "", err
	}
	var result struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("foundrydb: decode DeleteAppServiceAuthUser response: %w", err)
	}
	if result.TaskID == "" {
		return "", fmt.Errorf("foundrydb: DeleteAppServiceAuthUser response missing task_id")
	}
	return result.TaskID, nil
}

// DeleteAppServiceAuthUserByIdentifier erases one end-user under the GDPR right
// to erasure (Art. 17), addressed by a single path identifier that is either an
// email address (contains '@') or a user UUID. It calls
// DELETE /app-services/{id}/auth/users/{identifier} and returns the task id for
// status polling. The identifier is percent-encoded before being placed in the
// URL path so email addresses with '+' or other special characters are handled
// correctly. The email is never persisted or logged controller-side.
func (c *Client) DeleteAppServiceAuthUserByIdentifier(ctx context.Context, appServiceID, identifier string) (string, error) {
	path := "/app-services/" + appServiceID + "/auth/users/" + url.PathEscape(identifier)
	resp, err := c.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return "", err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return "", err
	}
	var result struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("foundrydb: decode DeleteAppServiceAuthUserByIdentifier response: %w", err)
	}
	if result.TaskID == "" {
		return "", fmt.Errorf("foundrydb: DeleteAppServiceAuthUserByIdentifier response missing task_id")
	}
	return result.TaskID, nil
}

// UpsertAppServiceAuthProviderRequest is the body for UpsertAppServiceAuthProvider.
// ClientSecret is write-only: it is stored in the platform secret store and
// never returned by any response. DisplayName is optional and sets the button
// label on the hosted login page (defaults to the provider name when omitted).
type UpsertAppServiceAuthProviderRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DisplayName  string `json:"display_name,omitempty"`
}

// AuthIDPProvidersResponse is the response envelope for the provider management
// endpoints. Providers carries the full list of configured social-login
// providers for the app after the operation is applied, without secrets.
type AuthIDPProvidersResponse struct {
	Providers []AuthIDPProviderConfig `json:"providers"`
}

// ListAppServiceAuthProviders returns the configured social-login providers for
// an app service without their secrets (provider id, client_id, and optional
// display_name only). Returns an empty slice when no providers are configured.
func (c *Client) ListAppServiceAuthProviders(ctx context.Context, appServiceID string) ([]AuthIDPProviderConfig, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/auth/providers", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AuthIDPProvidersResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppServiceAuthProviders response: %w", err)
	}
	return result.Providers, nil
}

// UpsertAppServiceAuthProvider adds or updates one social-login provider for an
// app service and returns the full list of configured providers after the
// operation. The provider is the lowercase provider id ("google" or "github");
// it is percent-encoded before being placed in the URL path. ClientSecret is
// write-only: it is stored in the platform secret store and never returned.
// When an Active auth configuration is updated, the issuer redeploys
// automatically to pick up the new credentials.
func (c *Client) UpsertAppServiceAuthProvider(ctx context.Context, appServiceID, provider string, req UpsertAppServiceAuthProviderRequest) ([]AuthIDPProviderConfig, error) {
	path := "/app-services/" + appServiceID + "/auth/providers/" + url.PathEscape(provider)
	resp, err := c.do(ctx, http.MethodPut, path, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AuthIDPProvidersResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpsertAppServiceAuthProvider response: %w", err)
	}
	return result.Providers, nil
}

// RemoveAppServiceAuthProvider removes one social-login provider from an app
// service and returns the remaining configured providers. The provider id is
// percent-encoded before being placed in the URL path. When an Active auth
// configuration is updated, the issuer redeploys automatically; a non-Active
// configuration picks up the change on its next deploy.
func (c *Client) RemoveAppServiceAuthProvider(ctx context.Context, appServiceID, provider string) ([]AuthIDPProviderConfig, error) {
	path := "/app-services/" + appServiceID + "/auth/providers/" + url.PathEscape(provider)
	resp, err := c.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AuthIDPProvidersResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RemoveAppServiceAuthProvider response: %w", err)
	}
	return result.Providers, nil
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

// AttachmentCatalogEntry is one installable companion app in the catalog: a
// curated app (for example Metabase) that can be attached to a database service
// by its Kind. ParentEngines lists the database engines it supports.
type AttachmentCatalogEntry struct {
	Kind          string   `json:"kind"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Category      string   `json:"category"`
	DefaultPlan   string   `json:"default_plan"`
	ParentEngines []string `json:"parent_engines"`
	// RequiredTasks lists the inference serving tasks the app can consume (for
	// example "generate" for a chat app). Attaching the app to an inference
	// parent whose model serves a task outside this list is rejected. Empty when
	// any task is accepted or the app does not wire to inference.
	RequiredTasks []string `json:"required_tasks,omitempty"`
	// Compatible is present only when the catalog was requested scoped to a
	// parent service (GetAttachmentCatalogForParent): whether this app can
	// attach to that parent (engine fit plus, for inference parents,
	// serving-task fit). Nil on the unscoped listing.
	Compatible *bool `json:"compatible,omitempty"`
	// IncompatibleReason is present only when Compatible is false: the same
	// customer-readable reason the attach and preview gates return.
	IncompatibleReason string `json:"incompatible_reason,omitempty"`
}

type attachmentCatalogResponse struct {
	Attachments []AttachmentCatalogEntry `json:"attachments"`
}

// GetAttachmentCatalog lists the installable companion apps. The catalog is
// static and read-only; each Kind is a value accepted by CreateAttachment.
func (c *Client) GetAttachmentCatalog(ctx context.Context) ([]AttachmentCatalogEntry, error) {
	return c.GetAttachmentCatalogForParent(ctx, "")
}

// GetAttachmentCatalogForParent lists the installable companion apps scoped to
// a parent service. With a non-empty parentID each entry carries a
// server-computed Compatible verdict and, when incompatible, the same
// IncompatibleReason the attach and preview gates return, so a gallery can dim
// incompatible apps up front. An unknown or inaccessible parent id degrades to
// the unscoped listing rather than erroring; an empty parentID returns the
// unscoped listing.
func (c *Client) GetAttachmentCatalogForParent(ctx context.Context, parentID string) ([]AttachmentCatalogEntry, error) {
	path := "/attachment-catalog"
	if parentID != "" {
		path += "?parent_id=" + url.QueryEscape(parentID)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result attachmentCatalogResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAttachmentCatalog response: %w", err)
	}
	return result.Attachments, nil
}

// CreateAttachmentRequest is the body of CreateAttachment. Only Kind is
// required; PlanName and Subdomain override the catalog descriptor's defaults.
type CreateAttachmentRequest struct {
	Kind      string `json:"kind"`
	PlanName  string `json:"plan_name,omitempty"`
	Subdomain string `json:"subdomain,omitempty"`
}

// CreateAttachment provisions a companion app from the catalog against a parent
// database service and links it over a private SDN. serviceID is the parent
// database service id. Returns the created app service, whose lifecycle is then
// managed through the app-service methods keyed by its id.
func (c *Client) CreateAttachment(ctx context.Context, serviceID string, req CreateAttachmentRequest) (*AppService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/managed-services/"+serviceID+"/attachments", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var app AppService
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateAttachment response: %w", err)
	}
	return &app, nil
}

// AttachmentSummary is one companion app attached to a database service: the
// app service id and kind, its status, and the attachment wiring status.
type AttachmentSummary struct {
	AttachmentID string `json:"attachment_id"`
	AppServiceID string `json:"app_service_id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	WiringStatus string `json:"wiring_status"`
	URL          string `json:"url,omitempty"`
}

type listAttachmentsResponse struct {
	Attachments []AttachmentSummary `json:"attachments"`
}

// ListAttachments lists the companion apps attached to a database service.
// serviceID is the parent database service id.
func (c *Client) ListAttachments(ctx context.Context, serviceID string) ([]AttachmentSummary, error) {
	resp, err := c.do(ctx, http.MethodGet, "/managed-services/"+serviceID+"/attachments", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listAttachmentsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAttachments response: %w", err)
	}
	return result.Attachments, nil
}

// AttachmentPreviewLine is one priced piece of a complete-solution attach: the
// app itself (Role "app") or one companion database (Role "companion"), each
// with its monthly compute and storage cost split out.
type AttachmentPreviewLine struct {
	Role        string `json:"role"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Plan        string `json:"plan"`
	StorageGB   int    `json:"storage_gb"`
	StorageTier string `json:"storage_tier"`
	// Zone the piece will be provisioned in; a companion may land in a different
	// zone than the app when its engine's template coverage differs.
	Zone string `json:"zone,omitempty"`
	// Purpose is why the solution needs this companion (companion lines only).
	Purpose            string  `json:"purpose,omitempty"`
	MonthlyComputeCost float64 `json:"monthly_compute_cost"`
	MonthlyStorageCost float64 `json:"monthly_storage_cost"`
	MonthlyTotalCost   float64 `json:"monthly_total_cost"`
	// DatabaseType is the companion's database engine (companion lines only).
	DatabaseType string `json:"database_type,omitempty"`
}

// AttachmentPreview is the pricing preview of a (complete-solution) attach,
// before anything is provisioned: the zone the pieces land in and a monthly
// cost line for the app plus every companion database the catalog descriptor
// declares (a companion the parent itself provides is omitted).
type AttachmentPreview struct {
	Kind             string                  `json:"kind"`
	DisplayName      string                  `json:"display_name"`
	Zone             string                  `json:"zone"`
	Currency         string                  `json:"currency"`
	Lines            []AttachmentPreviewLine `json:"lines"`
	MonthlyTotalCost float64                 `json:"monthly_total_cost"`
}

// PreviewAttachment prices attaching the given catalog kind to a parent service
// before anything is provisioned. serviceID is the parent database or inference
// service id; kind is a value from GetAttachmentCatalog. Prices come from the
// same plan and storage-pricing tables as the standalone /plans figures.
// Read-only: nothing is created.
func (c *Client) PreviewAttachment(ctx context.Context, serviceID, kind string) (*AttachmentPreview, error) {
	path := "/managed-services/" + serviceID + "/attachments/preview?kind=" + url.QueryEscape(kind)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var preview AttachmentPreview
	if err := json.Unmarshal(data, &preview); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PreviewAttachment response: %w", err)
	}
	return &preview, nil
}

// AttachmentCredentials is the generated admin login for a catalog attachment's
// companion app, plus the login URL. An app whose admin is created by a
// post-deploy hook (for example Metabase) reports it in AdminEmail and
// AdminPassword; an app that bootstraps its admin from environment (for example
// Directus) reports the reveal-flagged generated values in Generated (for
// example ADMIN_EMAIL and ADMIN_PASSWORD). Every secret is decrypted on demand
// and returned only here.
type AttachmentCredentials struct {
	AdminEmail    string `json:"admin_email,omitempty"`
	AdminPassword string `json:"admin_password,omitempty"`
	// Generated maps each reveal-flagged generated environment key to its
	// decrypted value, for an attachment whose admin is bootstrapped from
	// environment rather than a post-deploy hook.
	Generated map[string]string `json:"generated,omitempty"`
	LoginURL  string            `json:"login_url,omitempty"`
}

// GetAttachmentCredentials reveals the generated admin credential for a catalog
// attachment's companion app. appServiceID is the companion app service id.
// Only catalog attachments carry generated credentials; otherwise the API
// returns 404.
func (c *Client) GetAttachmentCredentials(ctx context.Context, appServiceID string) (*AttachmentCredentials, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/attachment-credentials", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var creds AttachmentCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAttachmentCredentials response: %w", err)
	}
	return &creds, nil
}

// AppSourceUpload is a source tarball held in the control plane only long
// enough for a build to consume it.
type AppSourceUpload struct {
	// UploadRef names this upload. Set it as AppSource.UploadRef, with Type
	// "upload", when creating the app.
	UploadRef string `json:"upload_ref"`
	// SizeBytes is the size of the stored tarball.
	SizeBytes int64 `json:"size_bytes"`
	// ChecksumSHA256 is taken over the uploaded bytes. The builder verifies the
	// same value after fetching the source, so comparing it against a local
	// checksum catches a corrupted transfer before a build is started.
	ChecksumSHA256 string `json:"checksum_sha256"`
	// ExpiresAt is when the upload is swept, 24 hours after it was created.
	ExpiresAt time.Time `json:"expires_at"`
}

// UploadAppSource uploads a gzipped source tarball and returns the reference an
// app create names as AppSource.UploadRef (with Type "upload"). Use it to
// deploy a local directory that the platform cannot reach as a git repository.
//
// The reader must yield a gzipped tarball; the API checks rather than assumes
// it. Pack the source under a single top-level directory, since the builder
// strips that one wrapping directory so the Dockerfile lands at the build root.
//
// The upload expires 24 hours after creation, so create the app from it in the
// same session. It is retained briefly after a build consumes it, so a failed
// build can be retried without uploading again.
func (c *Client) UploadAppSource(ctx context.Context, tarball io.Reader) (*AppSourceUpload, error) {
	if tarball == nil {
		return nil, fmt.Errorf("foundrydb: UploadAppSource requires a tarball")
	}
	resp, err := c.doRaw(ctx, http.MethodPost, "/app-source-uploads", tarball, "application/octet-stream", "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AppSourceUpload
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UploadAppSource response: %w", err)
	}
	return &result, nil
}

// AppNode is one VM behind an app service.
type AppNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// Role distinguishes members once an app runs on more than one VM. Empty
	// while the app is single-node.
	Role string `json:"role,omitempty"`
	// AgentVersion is the build of the agent running on this VM. The agent runs
	// from a binary baked into the VM template, so it can lag the controller;
	// this is what makes that visible. Empty until the agent has registered.
	AgentVersion string `json:"agent_version,omitempty"`
	// PublicIP is the app's floating IP, the address its DNS resolves to. The
	// VM's baseline public address and utility address are not reported.
	PublicIP     string     `json:"public_ip,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
}

type listAppNodesResponse struct {
	Nodes []AppNode `json:"nodes"`
}

// ListAppNodes returns the VMs behind an app service.
func (c *Client) ListAppNodes(ctx context.Context, appServiceID string) ([]AppNode, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/nodes", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listAppNodesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppNodes response: %w", err)
	}
	return result.Nodes, nil
}

// AppScanPolicy decides whether vulnerability findings block a build.
type AppScanPolicy string

const (
	// AppScanPolicyRecord scans and records findings without blocking.
	AppScanPolicyRecord AppScanPolicy = ""
	// AppScanPolicyBlockCritical fails a build with critical findings.
	AppScanPolicyBlockCritical AppScanPolicy = "block-critical"
	// AppScanPolicyBlockHigh fails a build with high or critical findings.
	AppScanPolicyBlockHigh AppScanPolicy = "block-high"
)
