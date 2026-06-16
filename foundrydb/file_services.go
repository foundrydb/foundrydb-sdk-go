package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FilesBucket describes one bucket backing a files service: where it lives and
// how S3 clients reach it.
type FilesBucket struct {
	// Region is the provider region hosting the bucket (e.g. "europe-1").
	Region string `json:"region"`
	// Bucket is the bucket name on the object storage instance. Use it
	// together with Endpoint when configuring an S3 client with a files
	// access key.
	Bucket string `json:"bucket"`
	// Endpoint is the S3 endpoint URL clients and the presigner use.
	Endpoint string `json:"endpoint"`
}

// FilesConfig is the per-service configuration of a files service: the buckets
// backing it, quota settings, data-protection flags, and the most recent usage
// measurement. It carries no secret material.
type FilesConfig struct {
	// Buckets lists the buckets backing this service (currently exactly one).
	Buckets []FilesBucket `json:"buckets"`
	// QuotaGBSoft is the stored-GB threshold that triggers a notification when
	// crossed.
	QuotaGBSoft int `json:"quota_gb_soft"`
	// QuotaGBHard is the stored-GB ceiling: once exceeded, new upload presigns
	// and key creation are blocked (reads continue so data can be evacuated).
	QuotaGBHard int `json:"quota_gb_hard"`
	// Versioning reports whether S3 object versioning is enabled on the bucket.
	Versioning bool `json:"versioning"`
	// SSE reports whether server-side encryption is enabled on the bucket.
	SSE bool `json:"sse"`
	// LifecycleEnabled reports whether the default noncurrent-version expiry
	// lifecycle rule is active on the bucket. False when the object storage
	// endpoint does not implement PutBucketLifecycleConfiguration.
	LifecycleEnabled bool `json:"lifecycle_enabled"`
	// MeasuredBytes is the bucket size from the most recent usage poll.
	MeasuredBytes int64 `json:"measured_bytes"`
	// MeasuredAt is when MeasuredBytes was captured; nil before the first poll.
	MeasuredAt *time.Time `json:"measured_at,omitempty"`
	// OverQuota reports whether the measured usage exceeds the hard quota.
	OverQuota bool `json:"over_quota"`
}

// FilesService is a managed object storage bucket service: an S3-compatible
// bucket with scoped access keys, presigned URLs, and quota enforcement.
// Provisioning is asynchronous: the service is created in the Pending status
// and reaches Running once the bucket exists; use WaitForFilesRunning to block
// until it is usable.
type FilesService struct {
	ID             string       `json:"id"`
	UserID         string       `json:"user_id"`
	OrganizationID string       `json:"organization_id,omitempty"`
	Name           string       `json:"name"`
	ServiceKind    string       `json:"service_kind"`
	Status         string       `json:"status"`
	Zone           string       `json:"zone"`
	FilesConfig    *FilesConfig `json:"files_config,omitempty"`
	CreatedAt      string       `json:"created_at"`
	UpdatedAt      string       `json:"updated_at"`
}

// CreateFilesServiceRequest is the body for CreateFilesService.
type CreateFilesServiceRequest struct {
	Name string `json:"name"`
	// Zone selects the provider region for the bucket; empty uses the platform
	// default.
	Zone string `json:"zone,omitempty"`
	// QuotaGBSoft and QuotaGBHard override the default storage quotas; nil
	// applies the platform defaults.
	QuotaGBSoft *int `json:"quota_gb_soft,omitempty"`
	QuotaGBHard *int `json:"quota_gb_hard,omitempty"`
	// OrganizationID optionally assigns the service to an organization the
	// requesting user belongs to.
	OrganizationID string `json:"organization_id,omitempty"`
}

// FilesAccessKey is one scoped S3 credential minted for a files service. Only
// the public half of the credential pair (AccessKeyID) is ever returned by
// list and get operations; the secret half is returned exactly once at
// creation time and cannot be retrieved afterwards.
type FilesAccessKey struct {
	ID             string `json:"id"`
	ServiceID      string `json:"service_id"`
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id,omitempty"`
	// Name is the user-facing label for the key.
	Name string `json:"name"`
	// AccessKeyID is the public half of the credential pair.
	AccessKeyID string `json:"access_key_id"`
	// Prefix is the object key prefix the credential is scoped to. Empty means
	// the whole bucket.
	Prefix string `json:"prefix"`
	// Permissions is the access level the key grants: "read", "write", or
	// "readwrite".
	Permissions string `json:"permissions"`
	// Purpose records why the key exists: "user" for customer-managed keys,
	// "attachment" for keys minted automatically for an app attachment.
	Purpose string `json:"purpose"`
	// Status is "active" or "revoked".
	Status     string     `json:"status"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateFilesAccessKeyRequest is the body for CreateFilesAccessKey.
type CreateFilesAccessKeyRequest struct {
	Name string `json:"name"`
	// Prefix scopes the key to an object key prefix; empty grants the whole
	// bucket.
	Prefix string `json:"prefix,omitempty"`
	// Permissions is "read", "write", or "readwrite".
	Permissions string `json:"permissions"`
}

// FilesAccessKeyWithSecret is the CreateFilesAccessKey response. The secret is
// returned exactly once, here at creation time: it is never persisted by the
// platform and there is no reveal endpoint, so a lost secret means rotating
// the key.
type FilesAccessKeyWithSecret struct {
	FilesAccessKey
	SecretAccessKey string `json:"secret_access_key"`
}

// PresignFilesURLRequest is the body for PresignFilesURL.
type PresignFilesURLRequest struct {
	// Method is the HTTP method to presign: GET, PUT, HEAD, or DELETE
	// (uppercase).
	Method string `json:"method"`
	// Key is the object key the URL operates on.
	Key string `json:"key"`
	// ExpiresSeconds bounds the URL lifetime; zero applies the platform
	// default (15 minutes), the maximum is 604800 (7 days).
	ExpiresSeconds int `json:"expires_seconds,omitempty"`
	// ContentType, when set on a PUT, is signed into the URL so the upload
	// must send the same Content-Type header.
	ContentType string `json:"content_type,omitempty"`
}

// FilesPresignedURL carries a presigned URL and its validity window. The URL
// runs directly against the bucket endpoint; no platform credentials are
// needed to use it.
type FilesPresignedURL struct {
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
}

// FilesObject is one object in a bucket listing.
type FilesObject struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
	ETag         string    `json:"etag"`
}

// FilesObjectPage is one page of a bucket listing. A non-empty NextCursor
// means more objects follow; pass it as the cursor of the next call.
type FilesObjectPage struct {
	Objects    []FilesObject `json:"objects"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type listFilesServicesResponse struct {
	FileServices []FilesService `json:"file_services"`
}

// ListFilesServices returns all files services visible to the authenticated
// user.
func (c *Client) ListFilesServices(ctx context.Context) ([]FilesService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/file-services", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listFilesServicesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListFilesServices response: %w", err)
	}
	return result.FileServices, nil
}

// GetFilesService returns the files service with the given UUID, including its
// bucket configuration, quotas, and measured usage. Returns nil, nil when it
// does not exist (404).
func (c *Client) GetFilesService(ctx context.Context, id string) (*FilesService, error) {
	resp, err := c.do(ctx, http.MethodGet, "/file-services/"+id, nil, "")
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
	var service FilesService
	if err := json.Unmarshal(data, &service); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GetFilesService response: %w", err)
	}
	return &service, nil
}

// CreateFilesService provisions a new files service (an S3-compatible bucket)
// and returns its initial state. The service is created in the Pending status;
// use WaitForFilesRunning to block until the bucket is provisioned.
func (c *Client) CreateFilesService(ctx context.Context, req CreateFilesServiceRequest) (*FilesService, error) {
	resp, err := c.do(ctx, http.MethodPost, "/file-services", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var service FilesService
	if err := json.Unmarshal(data, &service); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateFilesService response: %w", err)
	}
	return &service, nil
}

// DeleteFilesService initiates deletion of the files service: the bucket
// contents, the bucket itself, and every credential minted for the service are
// removed. A 404 response is treated as success (idempotent).
func (c *Client) DeleteFilesService(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/file-services/"+id, nil, "")
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

// CreateFilesAccessKey mints a new scoped S3 credential for the files service
// and returns it together with the secret access key. The secret is returned
// exactly once, in this response: store it immediately. Key creation is
// blocked while the service is over its hard storage quota.
func (c *Client) CreateFilesAccessKey(ctx context.Context, serviceID string, req CreateFilesAccessKeyRequest) (*FilesAccessKeyWithSecret, error) {
	resp, err := c.do(ctx, http.MethodPost, "/file-services/"+serviceID+"/keys", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var key FilesAccessKeyWithSecret
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, fmt.Errorf("foundrydb: decode CreateFilesAccessKey response: %w", err)
	}
	return &key, nil
}

type listFilesAccessKeysResponse struct {
	Keys []FilesAccessKey `json:"keys"`
}

// ListFilesAccessKeys returns the service's access keys. Secret halves are
// never included; they are returned only by CreateFilesAccessKey.
func (c *Client) ListFilesAccessKeys(ctx context.Context, serviceID string) ([]FilesAccessKey, error) {
	resp, err := c.do(ctx, http.MethodGet, "/file-services/"+serviceID+"/keys", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listFilesAccessKeysResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListFilesAccessKeys response: %w", err)
	}
	return result.Keys, nil
}

// RevokeFilesAccessKey revokes one access key end to end: the provider
// credential is deleted and the stored secret is destroyed. Revocation is
// permanent; mint a new key to restore access.
func (c *Client) RevokeFilesAccessKey(ctx context.Context, serviceID, keyID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/file-services/"+serviceID+"/keys/"+keyID, nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// PresignFilesURL asks the platform to presign one S3 operation against the
// service's bucket and returns the URL. The URL is used directly against the
// bucket endpoint without further credentials, until it expires. Upload (PUT)
// presigning is blocked while the service is over its hard storage quota.
func (c *Client) PresignFilesURL(ctx context.Context, serviceID string, req PresignFilesURLRequest) (*FilesPresignedURL, error) {
	resp, err := c.do(ctx, http.MethodPost, "/file-services/"+serviceID+"/presign", req, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var presigned FilesPresignedURL
	if err := json.Unmarshal(data, &presigned); err != nil {
		return nil, fmt.Errorf("foundrydb: decode PresignFilesURL response: %w", err)
	}
	return &presigned, nil
}

// ListFilesObjects returns one page of the bucket's objects, optionally
// filtered by key prefix. max bounds the page size (0 applies the platform
// default of 100, the maximum is 1000); pass the previous page's NextCursor as
// cursor to continue the listing.
func (c *Client) ListFilesObjects(ctx context.Context, serviceID, prefix, cursor string, max int) (*FilesObjectPage, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if max > 0 {
		q.Set("max", strconv.Itoa(max))
	}
	path := "/file-services/" + serviceID + "/objects"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var page FilesObjectPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListFilesObjects response: %w", err)
	}
	return &page, nil
}

// DeleteFilesObject removes one object from the service's bucket.
func (c *Client) DeleteFilesObject(ctx context.Context, serviceID, key string) error {
	q := url.Values{}
	q.Set("key", key)
	resp, err := c.do(ctx, http.MethodDelete, "/file-services/"+serviceID+"/objects?"+q.Encode(), nil, "")
	if err != nil {
		return err
	}
	_, err = checkResponse(resp)
	return err
}

// WaitForFilesRunning polls the files service until it reaches "Running"
// status or the timeout expires. Polling interval is 5 seconds (bucket
// provisioning is fast). The context deadline (if any) takes precedence over
// timeout. Returns an error immediately when the service enters a terminal
// failure state.
func (c *Client) WaitForFilesRunning(ctx context.Context, id string, timeout time.Duration) (*FilesService, error) {
	deadline := time.Now().Add(timeout)
	for {
		service, err := c.GetFilesService(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("foundrydb: polling files service %s: %w", id, err)
		}
		if service == nil {
			return nil, fmt.Errorf("foundrydb: files service %s not found while waiting for running status", id)
		}

		status := strings.ToLower(service.Status)
		if status == "running" {
			return service, nil
		}
		if strings.Contains(status, "failed") || status == "error" {
			return nil, fmt.Errorf("foundrydb: files service %s entered terminal status %q", id, service.Status)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("foundrydb: timed out after %s waiting for files service %s to reach running status (current: %s)",
				timeout, id, service.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
