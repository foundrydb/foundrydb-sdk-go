package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// AppJob is a job definition on an app service: a container run (image,
// command, and environment layered over the app's own configuration) with an
// optional cron schedule. A nil ScheduleCron means the job only runs when
// invoked explicitly via RunAppJob.
type AppJob struct {
	ID        string `json:"id"`
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`

	// ScheduleCron is a five-field cron expression (minute granularity,
	// descriptors like @daily accepted) evaluated in Timezone. Nil means the
	// job has no schedule.
	ScheduleCron *string `json:"schedule_cron,omitempty"`
	Timezone     string  `json:"timezone"`
	Enabled      bool    `json:"enabled"`

	// ImageRef overrides the app's image for this job; nil inherits it.
	ImageRef *string `json:"image_ref,omitempty"`
	// Command is the container argv override (exec form, never a shell).
	Command []string `json:"command,omitempty"`
	// Env is layered over the app's environment (user env plus injected
	// connection variables) at dispatch time; job keys win.
	Env map[string]string `json:"env,omitempty"`

	MaxRetries          int    `json:"max_retries"`
	RetryBackoffSeconds int    `json:"retry_backoff_seconds"`
	MaxRuntimeSeconds   int    `json:"max_runtime_seconds"`
	ConcurrencyCap      int    `json:"concurrency_cap"`
	OverlapPolicy       string `json:"overlap_policy"`

	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// AppJobInvocation is one execution (or recorded skip) of a job. Status is one
// of queued, running, succeeded, failed, timed_out, or skipped.
type AppJobInvocation struct {
	ID                string     `json:"id"`
	JobID             string     `json:"job_id"`
	ServiceID         string     `json:"service_id"`
	Status            string     `json:"status"`
	Attempt           int        `json:"attempt"`
	TriggeredBy       string     `json:"triggered_by"`
	TriggeredByUserID *string    `json:"triggered_by_user_id,omitempty"`
	AgentTaskID       *string    `json:"agent_task_id,omitempty"`
	UnitName          *string    `json:"unit_name,omitempty"`
	ScheduledFor      *time.Time `json:"scheduled_for,omitempty"`
	QueuedAt          time.Time  `json:"queued_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	DurationMs        *int64     `json:"duration_ms,omitempty"`
	ExitCode          *int       `json:"exit_code,omitempty"`
	ErrorMessage      *string    `json:"error_message,omitempty"`
	// LogTail is the trailing log lines the agent captured into the
	// invocation result; full logs come from the invocation logs endpoints.
	LogTail       *string   `json:"log_tail,omitempty"`
	RetryEnqueued bool      `json:"retry_enqueued"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// AppJobCreateRequest is the body for CreateAppJob. Nil optional fields take
// the platform defaults (enabled, UTC, no retries, 1 hour max runtime,
// concurrency cap 1).
type AppJobCreateRequest struct {
	Name         string            `json:"name"`
	ScheduleCron *string           `json:"schedule_cron,omitempty"`
	Timezone     string            `json:"timezone,omitempty"`
	Enabled      *bool             `json:"enabled,omitempty"`
	ImageRef     *string           `json:"image_ref,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	MaxRetries   *int              `json:"max_retries,omitempty"`

	RetryBackoffSeconds *int `json:"retry_backoff_seconds,omitempty"`
	MaxRuntimeSeconds   *int `json:"max_runtime_seconds,omitempty"`
	ConcurrencyCap      *int `json:"concurrency_cap,omitempty"`
}

// AppJobPatchRequest is the body for UpdateAppJob. Nil fields keep the current
// value; ClearSchedule and ClearImageRef distinguish "leave alone" from
// "remove the override".
type AppJobPatchRequest struct {
	ScheduleCron  *string           `json:"schedule_cron,omitempty"`
	ClearSchedule bool              `json:"clear_schedule,omitempty"`
	Timezone      *string           `json:"timezone,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"`
	ImageRef      *string           `json:"image_ref,omitempty"`
	ClearImageRef bool              `json:"clear_image_ref,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	MaxRetries    *int              `json:"max_retries,omitempty"`

	RetryBackoffSeconds *int `json:"retry_backoff_seconds,omitempty"`
	MaxRuntimeSeconds   *int `json:"max_runtime_seconds,omitempty"`
	ConcurrencyCap      *int `json:"concurrency_cap,omitempty"`
}

// AppJobLogLines is the log payload inside a completed invocation logs fetch.
type AppJobLogLines struct {
	Lines       []string `json:"lines"`
	LogFilePath string   `json:"log_file_path"`
	TruncatedAt *int     `json:"truncated_at,omitempty"`
}

// AppJobInvocationLogs is the poll response for an invocation logs fetch task.
// Status mirrors the agent task lifecycle (PENDING, DISPATCHED, IN_PROGRESS,
// COMPLETED, FAILED, TIMEOUT, CANCELLED); Result is set once COMPLETED.
type AppJobInvocationLogs struct {
	TaskID       string          `json:"task_id"`
	Status       string          `json:"status"`
	Result       *AppJobLogLines `json:"result,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}

type listAppJobsResponse struct {
	Jobs []AppJob `json:"jobs"`
}

type listAppJobInvocationsResponse struct {
	Invocations []AppJobInvocation `json:"invocations"`
}

// CreateAppJob creates a job definition on an app service. A service supports
// up to 20 jobs; creating a second job with the same name returns a conflict.
func (c *Client) CreateAppJob(ctx context.Context, appServiceID string, req AppJobCreateRequest) (*AppJob, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/jobs", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var job AppJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateAppJob response: %w", err)
	}
	return &job, nil
}

// ListAppJobs returns the job definitions of an app service, oldest first.
func (c *Client) ListAppJobs(ctx context.Context, appServiceID string) ([]AppJob, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/jobs", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listAppJobsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppJobs response: %w", err)
	}
	return result.Jobs, nil
}

// GetAppJob returns one job definition.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetAppJob(ctx context.Context, appServiceID, jobID string) (*AppJob, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/jobs/"+jobID, nil, "")
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
	var job AppJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppJob response: %w", err)
	}
	return &job, nil
}

// UpdateAppJob applies a partial update to a job definition and returns the
// updated state. Editing the schedule, timezone, or enabled flag recomputes
// the next fire time from now.
func (c *Client) UpdateAppJob(ctx context.Context, appServiceID, jobID string, req AppJobPatchRequest) (*AppJob, error) {
	resp, err := c.do(ctx, http.MethodPatch, "/app-services/"+appServiceID+"/jobs/"+jobID, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var job AppJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("foundrydb: decode UpdateAppJob response: %w", err)
	}
	return &job, nil
}

// DeleteAppJob deletes a job definition together with its invocation history.
// A running invocation finishes on the VM but reports into the deleted
// history. A 404 response is treated as success (idempotent).
func (c *Client) DeleteAppJob(ctx context.Context, appServiceID, jobID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/app-services/"+appServiceID+"/jobs/"+jobID, nil, "")
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

// RunAppJob triggers a manual invocation of a job on a running app service and
// returns the queued invocation (202). When the job is already at its
// concurrency cap the API returns a conflict (409) as an *APIError; retry once
// a slot frees. Execution is asynchronous: poll GetAppJobInvocation until the
// status is terminal.
func (c *Client) RunAppJob(ctx context.Context, appServiceID, jobID string) (*AppJobInvocation, error) {
	resp, err := c.do(ctx, http.MethodPost, "/app-services/"+appServiceID+"/jobs/"+jobID+"/run", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var inv AppJobInvocation
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RunAppJob response: %w", err)
	}
	if inv.ID == "" {
		// The API falls back to {"invocation_id": ...} when the committed
		// invocation could not be read back; surface the ID either way.
		var fallback struct {
			InvocationID string `json:"invocation_id"`
		}
		if err := json.Unmarshal(data, &fallback); err == nil && fallback.InvocationID != "" {
			inv.ID = fallback.InvocationID
			inv.JobID = jobID
		}
	}
	return &inv, nil
}

// ListAppJobInvocations returns a job's invocation history, newest first.
// limit caps the page size (server default 50, max 200); offset skips that
// many rows. Pass 0 for either to take the server defaults.
func (c *Client) ListAppJobInvocations(ctx context.Context, appServiceID, jobID string, limit, offset int) ([]AppJobInvocation, error) {
	path := "/app-services/" + appServiceID + "/jobs/" + jobID + "/invocations"
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
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
	var result listAppJobInvocationsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListAppJobInvocations response: %w", err)
	}
	return result.Invocations, nil
}

// GetAppJobInvocation returns one invocation.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetAppJobInvocation(ctx context.Context, appServiceID, jobID, invocationID string) (*AppJobInvocation, error) {
	resp, err := c.do(ctx, http.MethodGet, "/app-services/"+appServiceID+"/jobs/"+jobID+"/invocations/"+invocationID, nil, "")
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
	var inv AppJobInvocation
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppJobInvocation response: %w", err)
	}
	return &inv, nil
}

// RequestAppJobInvocationLogs asks the app VM's agent to fetch the logs of one
// invocation (its transient systemd unit) and returns the task ID to poll with
// GetAppJobInvocationLogs (202). lines caps the tail length (server default
// 200, max 1000); pass 0 for the default. Invocations that never ran (skips)
// have no logs and return a bad request.
func (c *Client) RequestAppJobInvocationLogs(ctx context.Context, appServiceID, jobID, invocationID string, lines int) (string, error) {
	path := "/app-services/" + appServiceID + "/jobs/" + jobID + "/invocations/" + invocationID + "/logs"
	if lines > 0 {
		path += "?lines=" + strconv.Itoa(lines)
	}
	resp, err := c.do(ctx, http.MethodPost, path, nil, "")
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
		return "", fmt.Errorf("foundrydb: decode RequestAppJobInvocationLogs response: %w", err)
	}
	if result.TaskID == "" {
		return "", fmt.Errorf("foundrydb: RequestAppJobInvocationLogs response missing task_id")
	}
	return result.TaskID, nil
}

// GetAppJobInvocationLogs polls an invocation logs fetch task created by
// RequestAppJobInvocationLogs. While the agent is still working the response
// status is non-terminal (202 from the API); once COMPLETED the Result holds
// the log lines, and FAILED/TIMEOUT/CANCELLED set ErrorMessage.
func (c *Client) GetAppJobInvocationLogs(ctx context.Context, appServiceID, jobID, invocationID, taskID string) (*AppJobInvocationLogs, error) {
	path := "/app-services/" + appServiceID + "/jobs/" + jobID + "/invocations/" + invocationID + "/logs?task_id=" + url.QueryEscape(taskID)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result AppJobInvocationLogs
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetAppJobInvocationLogs response: %w", err)
	}
	return &result, nil
}
