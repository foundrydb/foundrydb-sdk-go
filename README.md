# foundrydb-sdk-go

Official Go SDK for the [FoundryDB](https://foundrydb.com) managed database platform.

Manage PostgreSQL, MySQL, MongoDB, Valkey, Kafka, OpenSearch, and MSSQL clusters programmatically using idiomatic Go with full `context.Context` support.

## Installation

```bash
go get github.com/anorph/foundrydb-sdk-go
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

    "github.com/anorph/foundrydb-sdk-go/foundrydb"
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
