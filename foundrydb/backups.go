package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ListBackups returns all backup records for the given managed service, newest first.
func (c *Client) ListBackups(ctx context.Context, serviceID string) ([]Backup, error) {
	path := "/managed-services/" + serviceID + "/backups"
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result ListBackupsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListBackups response: %w", err)
	}
	return result.Backups, nil
}

// triggerBackupResponse is the raw envelope returned by POST /managed-services/{id}/backups.
// The API returns backup_id instead of id, so we normalize it into a Backup.
type triggerBackupResponse struct {
	BackupID string       `json:"backup_id"`
	Status   BackupStatus `json:"status"`
	Message  string       `json:"message"`
	TaskID   string       `json:"task_id"`
}

// TriggerBackup requests an on-demand backup for the given managed service.
// Use req.BackupType to select "full", "incremental", or "pitr"; leave empty for the
// platform default (full).
func (c *Client) TriggerBackup(ctx context.Context, serviceID string, req CreateBackupRequest) (*Backup, error) {
	path := "/managed-services/" + serviceID + "/backups"
	resp, err := c.do(ctx, http.MethodPost, path, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var raw triggerBackupResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("foundrydb: decode TriggerBackup response: %w", err)
	}
	return &Backup{
		ID:         raw.BackupID,
		ServiceID:  serviceID,
		Status:     raw.Status,
		BackupType: req.BackupType,
	}, nil
}

// SetBackupDestination configures (or replaces) the per-service BYOB backup
// destination. Connectivity is validated with the supplied credentials before
// the destination is stored; an unreachable bucket or invalid credentials
// return an *APIError with StatusCode 400. The returned destination never
// includes the secret access key.
func (c *Client) SetBackupDestination(ctx context.Context, serviceID string, req SetBackupDestinationRequest) (*BackupDestination, error) {
	path := "/managed-services/" + serviceID + "/backup-destination"
	resp, err := c.do(ctx, http.MethodPut, path, req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var dest BackupDestination
	if err := json.Unmarshal(data, &dest); err != nil {
		return nil, fmt.Errorf("foundrydb: decode SetBackupDestination response: %w", err)
	}
	return &dest, nil
}

// GetBackupDestination returns the masked per-service backup destination
// (never the secret). Returns nil, nil when none is configured (404); the
// service uses the platform-managed bucket in that case.
func (c *Client) GetBackupDestination(ctx context.Context, serviceID string) (*BackupDestination, error) {
	path := "/managed-services/" + serviceID + "/backup-destination"
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
	var dest BackupDestination
	if err := json.Unmarshal(data, &dest); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetBackupDestination response: %w", err)
	}
	return &dest, nil
}

// DeleteBackupDestination removes the per-service backup destination,
// reverting the service to the platform-managed bucket. Returns an *APIError
// with StatusCode 404 when none is configured.
func (c *Client) DeleteBackupDestination(ctx context.Context, serviceID string) error {
	path := "/managed-services/" + serviceID + "/backup-destination"
	resp, err := c.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}
