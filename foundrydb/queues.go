package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Queue is a named message queue hosted on a PostgreSQL managed service. The
// durable state (messages) lives in the customer's database, transactional
// with their data; this resource tracks existence and settings. Status is one
// of Pending, Provisioning, Active, Deprovisioning, or Failed; brokered
// data-plane operations (enqueue, stats) require Active.
type Queue struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id,omitempty"`
	ServiceID      string `json:"service_id"`
	Name           string `json:"name"`
	DatabaseName   string `json:"database_name"`

	// VisibilityTimeoutSeconds is the redelivery horizon: how long a claimed
	// message stays invisible before a crashed consumer's claim expires.
	VisibilityTimeoutSeconds int `json:"visibility_timeout_seconds"`
	// MaxAttempts is how many deliveries a message gets before it is dropped
	// or dead-lettered (when DLQEnabled).
	MaxAttempts int  `json:"max_attempts"`
	DLQEnabled  bool `json:"dlq_enabled"`

	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// QueueCreateRequest is the body for CreateQueue. Nil optional fields take the
// platform defaults (defaultdb, 30 second visibility timeout, 5 attempts, DLQ
// enabled).
type QueueCreateRequest struct {
	Name                     string `json:"name"`
	DatabaseName             string `json:"database_name,omitempty"`
	VisibilityTimeoutSeconds *int   `json:"visibility_timeout_seconds,omitempty"`
	MaxAttempts              *int   `json:"max_attempts,omitempty"`
	DLQEnabled               *bool  `json:"dlq_enabled,omitempty"`
}

// QueueEnqueueMessage is one message in an enqueue batch.
type QueueEnqueueMessage struct {
	// Payload is an arbitrary JSON document.
	Payload map[string]interface{} `json:"payload"`
	// DelaySeconds postpones first visibility.
	DelaySeconds int `json:"delay_seconds,omitempty"`
}

// QueueEnqueueRequest is the body for EnqueueQueueMessages: a batch of up to
// 100 messages written to the queue in one transaction on the database VM.
type QueueEnqueueRequest struct {
	Messages []QueueEnqueueMessage `json:"messages"`
}

// QueueEnqueueMessageIDs is the result of a completed enqueue task: the IDs
// assigned to the batch, in request order.
type QueueEnqueueMessageIDs struct {
	MessageIDs []int64 `json:"message_ids"`
}

// QueueEnqueueResult is the poll response for an enqueue task. Status mirrors
// the agent task lifecycle (PENDING, DISPATCHED, IN_PROGRESS, COMPLETED);
// Result is set once COMPLETED. A failed task surfaces as an *APIError from
// GetEnqueueResult instead.
type QueueEnqueueResult struct {
	TaskID string                  `json:"task_id"`
	Status string                  `json:"status"`
	Result *QueueEnqueueMessageIDs `json:"result,omitempty"`
}

// QueueStats is the per-queue depth snapshot returned by a completed stats
// task.
type QueueStats struct {
	QueueName        string  `json:"queue_name"`
	ReadyMessages    int64   `json:"ready_messages"`
	InflightMessages int64   `json:"inflight_messages"`
	DeadMessages     int64   `json:"dead_messages"`
	OldestAgeSeconds float64 `json:"oldest_age_seconds"`
}

// QueueStatsResult is the poll response for a stats task. Status mirrors the
// agent task lifecycle; Result is set once COMPLETED. A failed task surfaces
// as an *APIError from GetQueueStats instead.
type QueueStatsResult struct {
	TaskID string      `json:"task_id"`
	Status string      `json:"status"`
	Result *QueueStats `json:"result,omitempty"`
}

type listQueuesResponse struct {
	Queues []Queue `json:"queues"`
}

type queueTaskResponse struct {
	TaskID string `json:"task_id"`
}

// CreateQueue creates a queue on a PostgreSQL managed service. Provisioning is
// asynchronous: the customer-side schema objects are created by an agent task,
// so the returned queue starts in the Provisioning status; poll GetQueue until
// it reaches Active. A service supports up to 50 queues.
func (c *Client) CreateQueue(ctx context.Context, serviceID string, req QueueCreateRequest) (*Queue, error) {
	resp, err := c.do(ctx, http.MethodPost, "/managed-services/"+serviceID+"/queues", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var q Queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateQueue response: %w", err)
	}
	return &q, nil
}

// ListQueues returns the service's queues, each reconciled against its pending
// provisioning task.
func (c *Client) ListQueues(ctx context.Context, serviceID string) ([]Queue, error) {
	resp, err := c.do(ctx, http.MethodGet, "/managed-services/"+serviceID+"/queues", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listQueuesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListQueues response: %w", err)
	}
	return result.Queues, nil
}

// GetQueue returns one queue by name, reconciled.
// Returns nil, nil when it does not exist (404).
func (c *Client) GetQueue(ctx context.Context, serviceID, queueName string) (*Queue, error) {
	resp, err := c.do(ctx, http.MethodGet, "/managed-services/"+serviceID+"/queues/"+url.PathEscape(queueName), nil, "")
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
	var q Queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetQueue response: %w", err)
	}
	return &q, nil
}

// DeleteQueue schedules asynchronous removal of a queue and returns it in the
// Deprovisioning status (202); the row disappears from ListQueues once the
// agent confirms the customer-side objects are gone. Pending messages are
// destroyed with the queue. Returns nil, nil when the queue does not exist
// (404, idempotent).
func (c *Client) DeleteQueue(ctx context.Context, serviceID, queueName string) (*Queue, error) {
	resp, err := c.do(ctx, http.MethodDelete, "/managed-services/"+serviceID+"/queues/"+url.PathEscape(queueName), nil, "")
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
	var q Queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("foundrydb: decode DeleteQueue response: %w", err)
	}
	return &q, nil
}

// EnqueueQueueMessages writes a batch of messages to an Active queue through a
// brokered agent task and returns the task ID to poll with GetEnqueueResult
// (202). The batch lands in one transaction, all-or-nothing. The round trip is
// bounded by the agent's poll interval, so this path suits low-rate external
// producers; platform apps enqueue directly over their injected connection.
func (c *Client) EnqueueQueueMessages(ctx context.Context, serviceID, queueName string, req QueueEnqueueRequest) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/managed-services/"+serviceID+"/queues/"+url.PathEscape(queueName)+"/messages", req, "")
	if err != nil {
		return "", err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return "", err
	}
	var result queueTaskResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("foundrydb: decode EnqueueQueueMessages response: %w", err)
	}
	if result.TaskID == "" {
		return "", fmt.Errorf("foundrydb: EnqueueQueueMessages response missing task_id")
	}
	return result.TaskID, nil
}

// GetEnqueueResult polls an enqueue task created by EnqueueQueueMessages.
// While the agent is still working the response status is non-terminal (202
// from the API); once COMPLETED the Result holds the assigned message IDs. A
// failed, timed out, or cancelled task returns an *APIError.
func (c *Client) GetEnqueueResult(ctx context.Context, serviceID, queueName, taskID string) (*QueueEnqueueResult, error) {
	path := "/managed-services/" + serviceID + "/queues/" + url.PathEscape(queueName) + "/messages?task_id=" + url.QueryEscape(taskID)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result QueueEnqueueResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetEnqueueResult response: %w", err)
	}
	return &result, nil
}

// RequestQueueStats creates a brokered depth-snapshot task for an Active queue
// and returns the task ID to poll with GetQueueStats (202).
func (c *Client) RequestQueueStats(ctx context.Context, serviceID, queueName string) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/managed-services/"+serviceID+"/queues/"+url.PathEscape(queueName)+"/stats", nil, "")
	if err != nil {
		return "", err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return "", err
	}
	var result queueTaskResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("foundrydb: decode RequestQueueStats response: %w", err)
	}
	if result.TaskID == "" {
		return "", fmt.Errorf("foundrydb: RequestQueueStats response missing task_id")
	}
	return result.TaskID, nil
}

// GetQueueStats polls a stats task created by RequestQueueStats. While the
// agent is still working the response status is non-terminal (202 from the
// API); once COMPLETED the Result holds the depth snapshot. A failed, timed
// out, or cancelled task returns an *APIError.
func (c *Client) GetQueueStats(ctx context.Context, serviceID, queueName, taskID string) (*QueueStatsResult, error) {
	path := "/managed-services/" + serviceID + "/queues/" + url.PathEscape(queueName) + "/stats?task_id=" + url.QueryEscape(taskID)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result QueueStatsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetQueueStats response: %w", err)
	}
	return &result, nil
}
