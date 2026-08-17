# foundrydb-sdk-go

Official Go SDK for the [FoundryDB](https://foundrydb.com) managed database platform.

Manage PostgreSQL, MySQL, MongoDB, Valkey, Kafka, OpenSearch, and MSSQL clusters programmatically using idiomatic Go with full `context.Context` support.

## Installation

```bash
go get github.com/foundrydb/foundrydb-sdk-go
```

Requires Go 1.21 or later.

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/foundrydb/foundrydb-sdk-go/foundrydb"
)

func main() {
    client := foundrydb.New(foundrydb.Config{
        APIURL:   "https://api.foundrydb.com",
        Username: "admin",
        Password: "yourpassword",
    })

    ctx := context.Background()

    // Create a PostgreSQL service
    storageSizeGB := 50
    svc, err := client.CreateService(ctx, foundrydb.CreateServiceRequest{
        Name:          "my-pg",
        DatabaseType:  foundrydb.PostgreSQL,
        Version:       "17",
        PlanName:      "tier-2",
        Zone:          "se-sto1",
        StorageSizeGB: &storageSizeGB,
        StorageTier:   string(foundrydb.StorageTierMaxIOPS),
    })
    if err != nil {
        log.Fatal(err)
    }

    // Wait until the service is ready to accept connections
    svc, err = client.WaitForRunning(ctx, svc.ID, 15*time.Minute)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Service running at ID: %s\n", svc.ID)
    if len(svc.DNSRecords) > 0 {
        fmt.Printf("Host: %s\n", svc.DNSRecords[0].FullDomain)
    }
}
```

## Configuration

Create a client with `foundrydb.New(foundrydb.Config{...})`:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `APIURL` | `string` | No | Base URL of the API. Defaults to `https://api.foundrydb.com`. |
| `Username` | `string` | Yes* | HTTP Basic Auth username. |
| `Password` | `string` | Yes* | HTTP Basic Auth password. |
| `Token` | `string` | Yes* | Bearer token. Takes precedence over Username/Password when set. |
| `OrgID` | `string` | No | Organization UUID. Sent as `X-Active-Org-ID` on every request. |
| `HTTPTimeout` | `time.Duration` | No | Per-request HTTP timeout. Defaults to 30 seconds. |

*Either `Username`+`Password` or `Token` must be provided.

## Supported Databases

| Constant | Engine | Supported Versions |
|----------|--------|-------------------|
| `foundrydb.PostgreSQL` | PostgreSQL | 14, 15, 16, 17, 18 |
| `foundrydb.MySQL` | MySQL | 8.4 |
| `foundrydb.MongoDB` | MongoDB | 6.0, 7.0, 8.0 |
| `foundrydb.Valkey` | Valkey | 7.2, 8.0, 8.1, 9.0 |
| `foundrydb.Kafka` | Apache Kafka | 3.6, 3.7, 3.8, 3.9, 4.0 |
| `foundrydb.OpenSearch` | OpenSearch | 2 |
| `foundrydb.MSSQL` | Microsoft SQL Server | 4.8 |

## Methods

### Services

#### `ListServices(ctx) ([]Service, error)`

Returns all services visible to the authenticated user. When `OrgID` is set on the client, only services belonging to that organization are returned.

#### `GetService(ctx, id string) (*Service, error)`

Returns the service with the given UUID. Returns `nil, nil` when the service does not exist.

#### `CreateService(ctx, CreateServiceRequest) (*Service, error)`

Provisions a new managed database service. The service will initially be in `provisioning` status. Use `WaitForRunning` to wait until it is ready.

**`CreateServiceRequest` fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Name` | `string` | Yes | Human-readable name. |
| `DatabaseType` | `DatabaseType` | Yes | Engine constant (e.g. `foundrydb.PostgreSQL`). |
| `Version` | `string` | No | Engine version (e.g. `"17"`). Uses platform default when empty. |
| `PlanName` | `string` | Yes | Compute tier: `tier-1` through `tier-15`. |
| `Zone` | `string` | No | UpCloud zone (e.g. `"se-sto1"`). Defaults to `se-sto1`. |
| `StorageSizeGB` | `*int` | No | Data disk size in GB. |
| `StorageTier` | `string` | No | `"maxiops"` (NVMe, production) or `"standard"` (HDD, dev). |
| `NodeCount` | `*int` | No | Number of nodes. 1 = single-node, 2+ = HA cluster. |
| `AutoFailoverEnabled` | `*bool` | No | Enable automatic failover for multi-node clusters. |
| `ReplicationMode` | `ReplicationMode` | No | `"async"` (default) or `"sync"`. |
| `EncryptionEnabled` | `*bool` | No | Enable encryption at rest for the data volume. |
| `AllowedCIDRs` | `[]string` | No | CIDR blocks permitted to connect (e.g. `["1.2.3.4/32"]`). |
| `MaintenanceWindow` | `string` | No | Preferred maintenance window. |

#### `UpdateService(ctx, id string, UpdateServiceRequest) (*Service, error)`

Applies a patch to an existing service and returns the updated state.

#### `DeleteService(ctx, id string) error`

Initiates deletion of a service. A 404 response is treated as success (idempotent).

#### `WaitForRunning(ctx, id string, timeout time.Duration) (*Service, error)`

Polls every 10 seconds until the service status is `"running"` or the timeout elapses. Returns an error immediately when the service enters a terminal failure state (`"failed"` or `"error"`).

### Organizations

#### `ListOrganizations(ctx) ([]Organization, error)`

Returns all organizations the authenticated user belongs to.

#### `GetOrganization(ctx, id string) (*Organization, error)`

Returns the organization with the given UUID. Returns `nil, nil` when not found.

### Users

#### `ListUsers(ctx, serviceID string) ([]DatabaseUser, error)`

Returns all database users defined on the given service.

#### `RevealPassword(ctx, serviceID, username string) (*RevealPasswordResponse, error)`

Returns the full connection credentials including the plaintext password and a ready-to-use connection string.

### Backups

#### `ListBackups(ctx, serviceID string) ([]Backup, error)`

Returns all backup records for the given service, newest first.

#### `TriggerBackup(ctx, serviceID string, CreateBackupRequest) (*Backup, error)`

Requests an on-demand backup. Set `BackupType` to `foundrydb.BackupTypeFull`, `foundrydb.BackupTypeIncremental`, or `foundrydb.BackupTypePITR`. Leave empty for the platform default.

### Compliance

Generate and retrieve signed compliance evidence packets for SOC 2 Type II and GDPR Article 30 (ROPA) reporting.

#### `GenerateComplianceReport(ctx, orgID, framework string) (*GenerateComplianceReportResponse, error)`

Requests a new signed evidence packet for the given organization. `framework` must be `"soc2"` or `"gdpr_ropa"`. The response embeds the full `CompliancePacket`, its Ed25519 detached signature, and a stable `ReportID` for later retrieval.

#### `ListComplianceReports(ctx, orgID string) ([]ComplianceReportRecord, error)`

Returns all previously generated compliance report records for the organization, newest first.

#### `DownloadComplianceReportJSON(ctx, orgID, reportID string) ([]byte, error)`

Returns the raw signed packet JSON for the given report. The Ed25519 signature inside the envelope can be verified against the keys published at `/.well-known/compliance-signing-keys`.

#### `DownloadComplianceReportPDF(ctx, orgID, reportID string) ([]byte, error)`

Returns the rendered PDF bytes for the given report. The PDF includes QR-encoded verification metadata for use in external audit workflows.

#### `ComplianceSigningKeys(ctx) (*ComplianceSigningKeySet, error)`

Returns the set of public keys used to sign compliance packets. This endpoint is unauthenticated and is suitable for use by external auditors.

#### `ListComplianceSubscriptions(ctx, orgID string) ([]ComplianceSubscription, error)`

Returns every supported framework with the organization's subscription status and monthly price.

#### `SubscribeComplianceFramework(ctx, orgID, framework string) ([]ComplianceSubscription, error)`

Enables a paid monthly subscription for the given framework (required to generate that framework's packets). Returns the updated subscription list.

#### `UnsubscribeComplianceFramework(ctx, orgID, framework string) ([]ComplianceSubscription, error)`

Disables a framework subscription. Returns the updated subscription list.

#### `RotateComplianceSigningKey(ctx) (*ComplianceSigningKeySet, error)`

Mints a new active signing key and retires the current one (admin only). Returns the published key set after rotation.

```go
// Generate a SOC 2 evidence packet
report, err := client.GenerateComplianceReport(ctx, orgID, "soc2")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Report ID: %s\n", report.ReportID)
fmt.Printf("Framework: %s\n", report.Packet.Framework)
fmt.Printf("Period: %s to %s\n", report.Packet.PeriodStart, report.Packet.PeriodEnd)

// Download the signed JSON for auditor verification
jsonBytes, err := client.DownloadComplianceReportJSON(ctx, orgID, report.ReportID)

// Download the human-readable PDF
pdfBytes, err := client.DownloadComplianceReportPDF(ctx, orgID, report.ReportID)
```

### Inference Services

A managed inference service is an open-weight LLM served by vLLM behind an OpenAI-compatible endpoint on the service's own hostname. There are two SKUs: `serverless` multiplexes onto a platform-owned shared GPU pool, takes no plan, is limited to curated models a pool already serves, and is billed per token; `dedicated` rents a whole-card GPU server, takes a GPU plan, serves curated or Hugging Face models, supports LoRA adapters and keep-warm, and is billed per GPU-hour.

Either way the customer calls `EndpointBaseURL` with an `fdb-inf` key, passing `foundrydb_managed/<served_model_name>` as the model.

#### `ListInferenceServices(ctx) ([]InferenceService, error)`

Returns the inference services visible to the authenticated user.

#### `GetInferenceService(ctx, id string) (*InferenceService, error)`

Returns one inference service, or `nil, nil` when it does not exist. While a deploy is in flight, `ProvisioningMessage` carries the live heartbeat.

#### `CreateInferenceService(ctx, InferenceServiceRequest) (*InferenceService, error)`

Provisions an inference service and returns it in the Pending status; poll until it reaches Running and `EndpointBaseURL` is set. A GPU `PlanName` creates a dedicated service; omitting it (or setting `InferenceSKU` to `serverless`) binds to the shared pool.

#### `CreateServerlessInferenceService(ctx, name, modelID, orgID string, licenseAccepted bool) (*InferenceService, error)`

Convenience over the above for the serverless SKU, which takes no plan, no zone, and no serving knobs.

#### `DeleteInferenceService(ctx, id string) error`

Tears the service down. A 404 is treated as success.

#### `ListServerlessInferenceModels(ctx) ([]ServerlessInferenceModel, error)`

Returns the curated models a serverless create can bind to right now. Ask this before a serverless create, which refuses any other model.

#### `ListInferenceModelRates(ctx) ([]InferenceModelRate, error)`

Returns the price in force right now for every priced curated model, so a create flow can quote before anyone commits. It is the same resolution the metering path uses.

#### `SwitchInferenceModel(ctx, id string, InferenceModelSwitchRequest) (*InferenceService, error)`

Changes which curated model an existing service serves, in place. The GPU server, plan, endpoint hostname, certificate, keys, and billing identity are unchanged.

#### `CheckInferenceFit(ctx, InferenceFitCheckRequest) (*InferenceFitCheckResult, error)`

Answers whether a model, at a context length, runs on a GPU plan, without provisioning or billing anything. A configuration that does not fit is still a successful call, and `Suggestions` names the closest fixes.

#### `GetInferenceServiceUsage(ctx, id, since string) (*InferenceServiceUsage, error)`

Returns metered usage and cost as a bucketed series with totals, plus the calendar-month-to-date rollup. `since` is a Go duration or an RFC 3339 start time; empty defaults to 24 hours.

#### `GetInferenceServiceMetrics(ctx, id, since string) (*InferenceServiceMetrics, error)`

Returns the live vLLM and GPU serving telemetry as a snapshot series with the newest reading broken out as `Latest`. `since` defaults to 30 minutes.

#### LoRA adapters

`RegisterInferenceAdapter`, `ListInferenceServiceAdapters`, `PromoteInferenceAdapter`, `DemoteInferenceAdapter`, and `DeleteInferenceAdapter` manage customer LoRA fine-tuned adapter versions in the serving registry. Promoting downloads the weights from Files, verifies their hash, and hot-loads them into vLLM with no restart; rollback is a promote of a prior version.

```go
// Quote, then create a serverless service on a model a pool already serves
models, err := client.ListServerlessInferenceModels(ctx)
if err != nil {
    log.Fatal(err)
}
rates, err := client.ListInferenceModelRates(ctx)

svc, err := client.CreateServerlessInferenceService(ctx, "my-llm", models[0].ModelID, orgID, true)
if err != nil {
    log.Fatal(err)
}

// Or check a dedicated configuration before committing a GPU to it
fit, err := client.CheckInferenceFit(ctx, foundrydb.InferenceFitCheckRequest{
    ModelSource: foundrydb.InferenceModelSourceCurated,
    ModelID:     "llama-3.1-70b-instruct",
    PlanName:    "gpu-l40s-1",
    MaxModelLen: 32768,
})
if !fit.Fits {
    for _, s := range fit.Suggestions {
        fmt.Println(s.Detail)
    }
}

fmt.Printf("Point your OpenAI SDK at %s\n", svc.EndpointBaseURL)
_ = rates
```

## Error Handling

All methods return a typed `*foundrydb.APIError` on non-2xx API responses. Use the helper functions to check specific conditions:

```go
svc, err := client.GetService(ctx, "nonexistent-id")
if foundrydb.IsNotFound(err) {
    fmt.Println("Service not found")
} else if err != nil {
    log.Fatal(err)
}
```

| Helper | HTTP Status |
|--------|-------------|
| `foundrydb.IsNotFound(err)` | 404 |
| `foundrydb.IsUnauthorized(err)` | 401 |
| `foundrydb.IsForbidden(err)` | 403 |

The raw status code and response body are available directly:

```go
if apiErr, ok := err.(*foundrydb.APIError); ok {
    fmt.Printf("status=%d body=%s\n", apiErr.StatusCode, apiErr.Body)
}
```

## Multi-Organization Usage

Scope all requests to a specific organization by setting `OrgID` in the config:

```go
orgClient := foundrydb.New(foundrydb.Config{
    Username: "admin",
    Password: "pass",
    OrgID:    "your-org-uuid",
})

// All calls below automatically include X-Active-Org-ID: your-org-uuid
services, _ := orgClient.ListServices(ctx)
```

To dynamically look up an organization UUID:

```go
orgs, _ := client.ListOrganizations(ctx)
for _, org := range orgs {
    fmt.Printf("%s: %s\n", org.Name, org.ID)
}
```

## Examples

The `examples/` directory contains runnable examples:

- `examples/basic/` - Create a PostgreSQL service, retrieve credentials, trigger a backup, then delete.
- `examples/multi-org/` - List organizations, create a Valkey service scoped to a team organization.

Run an example:

```bash
export FOUNDRYDB_USERNAME=admin
export FOUNDRYDB_PASSWORD=yourpassword
go run ./examples/basic/
```

## License

Apache 2.0. See [LICENSE](LICENSE).
