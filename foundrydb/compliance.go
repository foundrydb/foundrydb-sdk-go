package foundrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// generateComplianceReportRequest is the request body for POST /organizations/{orgID}/compliance-reports.
type generateComplianceReportRequest struct {
	Framework string `json:"framework"`
}

// listComplianceReportsResponse is the envelope returned by GET /organizations/{orgID}/compliance-reports.
type listComplianceReportsResponse struct {
	Reports []ComplianceReportRecord `json:"reports"`
}

// GenerateComplianceReport requests a new signed compliance evidence packet for the given
// organization and framework. framework must be "soc2" or "gdpr_ropa". The response
// includes the full packet, its Ed25519 signature, and a stable report ID that can be
// used with DownloadComplianceReportJSON and DownloadComplianceReportPDF.
func (c *Client) GenerateComplianceReport(ctx context.Context, orgID, framework string) (*GenerateComplianceReportResponse, error) {
	path := "/organizations/" + orgID + "/compliance-reports"
	body := generateComplianceReportRequest{Framework: framework}
	resp, err := c.do(ctx, http.MethodPost, path, body, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result GenerateComplianceReportResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode GenerateComplianceReport response: %w", err)
	}
	return &result, nil
}

// ListComplianceReports returns all previously generated compliance report records for
// the given organization, newest first.
func (c *Client) ListComplianceReports(ctx context.Context, orgID string) ([]ComplianceReportRecord, error) {
	path := "/organizations/" + orgID + "/compliance-reports"
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result listComplianceReportsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListComplianceReports response: %w", err)
	}
	return result.Reports, nil
}

// DownloadComplianceReportJSON returns the raw signed packet JSON bytes for the given
// compliance report. The caller can verify the Ed25519 signature contained in the
// envelope using the keys published at /.well-known/compliance-signing-keys.
func (c *Client) DownloadComplianceReportJSON(ctx context.Context, orgID, reportID string) ([]byte, error) {
	path := "/organizations/" + orgID + "/compliance-reports/" + reportID
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// DownloadComplianceReportPDF returns the raw PDF bytes for the given compliance report.
// The PDF is a human-readable rendering of the signed packet and includes QR-encoded
// verification metadata.
func (c *Client) DownloadComplianceReportPDF(ctx context.Context, orgID, reportID string) ([]byte, error) {
	path := "/organizations/" + orgID + "/compliance-reports/" + reportID + "/pdf"
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ComplianceSigningKeys returns the set of public keys used to sign compliance evidence
// packets. The endpoint is unauthenticated and suitable for use by external auditors.
func (c *Client) ComplianceSigningKeys(ctx context.Context) (*ComplianceSigningKeySet, error) {
	resp, err := c.do(ctx, http.MethodGet, "/.well-known/compliance-signing-keys", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result ComplianceSigningKeySet
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ComplianceSigningKeys response: %w", err)
	}
	return &result, nil
}

// complianceSubscriptionsResponse is the envelope for the subscription endpoints.
type complianceSubscriptionsResponse struct {
	Subscriptions []ComplianceSubscription `json:"subscriptions"`
}

// ListComplianceSubscriptions returns every supported framework with the
// organization's subscription status and monthly price.
func (c *Client) ListComplianceSubscriptions(ctx context.Context, orgID string) ([]ComplianceSubscription, error) {
	path := "/organizations/" + orgID + "/compliance-subscriptions"
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result complianceSubscriptionsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode ListComplianceSubscriptions response: %w", err)
	}
	return result.Subscriptions, nil
}

// SubscribeComplianceFramework enables a paid monthly subscription for the
// framework (required to generate that framework's packets). Returns the updated
// subscription list.
func (c *Client) SubscribeComplianceFramework(ctx context.Context, orgID, framework string) ([]ComplianceSubscription, error) {
	return c.setComplianceSubscription(ctx, orgID, framework, http.MethodPut)
}

// UnsubscribeComplianceFramework disables a framework subscription.
func (c *Client) UnsubscribeComplianceFramework(ctx context.Context, orgID, framework string) ([]ComplianceSubscription, error) {
	return c.setComplianceSubscription(ctx, orgID, framework, http.MethodDelete)
}

func (c *Client) setComplianceSubscription(ctx context.Context, orgID, framework, method string) ([]ComplianceSubscription, error) {
	path := "/organizations/" + orgID + "/compliance-subscriptions/" + framework
	resp, err := c.do(ctx, method, path, nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result complianceSubscriptionsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode compliance subscription response: %w", err)
	}
	return result.Subscriptions, nil
}

// RotateComplianceSigningKey mints a new active signing key and retires the
// current one (admin only). Returns the published key set after rotation.
func (c *Client) RotateComplianceSigningKey(ctx context.Context) (*ComplianceSigningKeySet, error) {
	resp, err := c.do(ctx, http.MethodPost, "/admin/compliance/signing-keys/rotate", nil, "")
	if err != nil {
		return nil, err
	}
	data, err := checkResponse(resp)
	if err != nil {
		return nil, err
	}
	var result ComplianceSigningKeySet
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("foundrydb: decode RotateComplianceSigningKey response: %w", err)
	}
	return &result, nil
}
