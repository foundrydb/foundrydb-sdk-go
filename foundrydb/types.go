// Package foundrydb provides a Go client for the FoundryDB managed database platform.
package foundrydb

// DatabaseType represents a supported database engine.
type DatabaseType string

const (
	// PostgreSQL is the PostgreSQL database engine.
	// Supported versions: 14, 15, 16, 17, 18
	PostgreSQL DatabaseType = "postgresql"
	// MySQL is the MySQL database engine.
	// Supported versions: 8.4
	MySQL DatabaseType = "mysql"
	// MongoDB is the MongoDB database engine.
	// Supported versions: 6.0, 7.0, 8.0
	MongoDB DatabaseType = "mongodb"
	// Valkey is the Valkey key-value store.
	// Supported versions: 7.2, 8.0, 8.1, 9.0
	Valkey DatabaseType = "valkey"
	// Kafka is the Apache Kafka event streaming platform.
	// Supported versions: 3.6, 3.7, 3.8, 3.9, 4.0
	Kafka DatabaseType = "kafka"
	// OpenSearch is the OpenSearch search and analytics engine.
	// Supported versions: 2
	OpenSearch DatabaseType = "opensearch"
	// MSSQL is Microsoft SQL Server.
	// Supported versions: 4.8
	MSSQL DatabaseType = "mssql"
)

// StorageTier represents the disk performance tier.
type StorageTier string

const (
	// StorageTierMaxIOPS is NVMe SSD storage, recommended for production.
	StorageTierMaxIOPS StorageTier = "maxiops"
	// StorageTierStandard is HDD-backed storage, suitable for development.
	StorageTierStandard StorageTier = "standard"
)

// ServiceStatus represents the lifecycle status of a managed service.
type ServiceStatus string

const (
	ServiceStatusPending      ServiceStatus = "pending"
	ServiceStatusProvisioning ServiceStatus = "provisioning"
	ServiceStatusRunning      ServiceStatus = "running"
	ServiceStatusStopped      ServiceStatus = "stopped"
	ServiceStatusError        ServiceStatus = "error"
	ServiceStatusDeleting     ServiceStatus = "deleting"
	ServiceStatusDeleted      ServiceStatus = "deleted"
)

// ReplicationMode controls how data is replicated across nodes.
type ReplicationMode string

const (
	ReplicationModeAsync ReplicationMode = "async"
	ReplicationModeSync  ReplicationMode = "sync"
)

// BackupStatus represents the lifecycle status of a backup.
type BackupStatus string

const (
	BackupStatusPending   BackupStatus = "pending"
	BackupStatusRunning   BackupStatus = "running"
	BackupStatusCompleted BackupStatus = "completed"
	BackupStatusFailed    BackupStatus = "failed"
)

// BackupType represents the type of a backup operation.
type BackupType string

const (
	BackupTypeFull        BackupType = "full"
	BackupTypeIncremental BackupType = "incremental"
	BackupTypePITR        BackupType = "pitr"
)

// DNSRecord is a DNS entry associated with a managed service.
type DNSRecord struct {
	FullDomain string `json:"full_domain"`
	RecordType string `json:"record_type"`
	Value      string `json:"value"`
}

// Service represents a managed database service returned by the API.
// The API returns the identifier as "id"; see TestServiceWireFormat which
// pins this tag so struct regenerations cannot silently regress it again.
type Service struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	DatabaseType        DatabaseType  `json:"database_type"`
	Version             string        `json:"version"`
	Status              ServiceStatus `json:"status"`
	PlanName            string        `json:"plan_name"`
	Zone                string        `json:"zone"`
	StorageSizeGB       int64         `json:"storage_size_gb"`
	StorageTier         StorageTier   `json:"storage_tier"`
	AllowedCIDRs        []string      `json:"allowed_cidrs"`
	DNSRecords          []DNSRecord   `json:"dns_records"`
	NodeCount           int           `json:"node_count"`
	AutoFailoverEnabled bool          `json:"auto_failover_enabled"`
	ReplicationMode     string        `json:"replication_mode"`
	EncryptionEnabled   bool          `json:"encryption_enabled"`
	MaintenanceWindow   string        `json:"maintenance_window"`
	CreatedAt           string        `json:"created_at"`
	UpdatedAt           string        `json:"updated_at"`
}

// ListServicesResponse is the envelope returned by GET /managed-services.
type ListServicesResponse struct {
	Services []Service `json:"services"`
}

// CreateServiceRequest is the request body for POST /managed-services.
type CreateServiceRequest struct {
	Name                string          `json:"name"`
	DatabaseType        DatabaseType    `json:"database_type"`
	Version             string          `json:"version,omitempty"`
	PlanName            string          `json:"plan_name"`
	Zone                string          `json:"zone,omitempty"`
	StorageSizeGB       *int            `json:"storage_size_gb,omitempty"`
	StorageTier         string          `json:"storage_tier,omitempty"`
	NodeCount           *int            `json:"node_count,omitempty"`
	AutoFailoverEnabled *bool           `json:"auto_failover_enabled,omitempty"`
	ReplicationMode     ReplicationMode `json:"replication_mode,omitempty"`
	EncryptionEnabled   *bool           `json:"encryption_enabled,omitempty"`
	AllowedCIDRs        []string        `json:"allowed_cidrs,omitempty"`
	MaintenanceWindow   string          `json:"maintenance_window,omitempty"`

	// Agent workload fields
	Preset           string            `json:"preset,omitempty"`              // Service preset (e.g., "agent-valkey-session")
	IsEphemeral      *bool             `json:"is_ephemeral,omitempty"`        // Mark as ephemeral
	TTLHours         *int              `json:"ttl_hours,omitempty"`           // Auto-delete after N hours (1-720)
	CreatedByAgentID string            `json:"created_by_agent_id,omitempty"` // Agent identifier
	AgentFramework   string            `json:"agent_framework,omitempty"`     // Framework: langchain, crewai, autogen, claude
	AgentPurpose     string            `json:"agent_purpose,omitempty"`       // Purpose: conversation_history, session_cache
	Labels           map[string]string `json:"labels,omitempty"`              // Custom key-value labels
}

// UpdateServiceRequest is the request body for PATCH /managed-services/{id}.
type UpdateServiceRequest struct {
	Name              *string  `json:"name,omitempty"`
	AllowedCIDRs      []string `json:"allowed_cidrs,omitempty"`
	MaintenanceWindow *string  `json:"maintenance_window,omitempty"`
}

// Organization represents a FoundryDB organization (personal or team).
type Organization struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	IsPersonal bool   `json:"is_personal"`
	Role       string `json:"role"`
	CreatedAt  string `json:"created_at"`
}

// ListOrganizationsResponse is the envelope returned by GET /organizations.
type ListOrganizationsResponse struct {
	Organizations []Organization `json:"organizations"`
}

// DatabaseUser represents a database user account on a managed service.
type DatabaseUser struct {
	Username  string   `json:"username"`
	Roles     []string `json:"roles"`
	CreatedAt string   `json:"created_at"`
}

// ListUsersResponse is the envelope returned by GET /managed-services/{id}/database-users.
type ListUsersResponse struct {
	Users []DatabaseUser `json:"users"`
}

// RevealPasswordResponse contains the full connection credentials for a database user.
type RevealPasswordResponse struct {
	Username         string `json:"username"`
	Password         string `json:"password"`
	Host             string `json:"host"`
	Port             int64  `json:"port"`
	Database         string `json:"database"`
	ConnectionString string `json:"connection_string"`
}

// Backup represents a backup record for a managed service.
type Backup struct {
	ID           string       `json:"id"`
	ServiceID    string       `json:"service_id"`
	Status       BackupStatus `json:"status"`
	BackupType   BackupType   `json:"backup_type"`
	SizeBytes    *int64       `json:"size_bytes"`
	CreatedAt    string       `json:"created_at"`
	CompletedAt  *string      `json:"completed_at"`
	ErrorMessage *string      `json:"error_message"`
}

// ListBackupsResponse is the envelope returned by GET /managed-services/{id}/backups.
type ListBackupsResponse struct {
	Backups []Backup `json:"backups"`
}

// CreateBackupRequest is the request body for POST /managed-services/{id}/backups.
type CreateBackupRequest struct {
	BackupType BackupType `json:"backup_type,omitempty"`
}
