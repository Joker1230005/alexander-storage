// Package cluster provides gRPC-based inter-node communication for
// multi-node Alexander Storage deployments.
package cluster

import (
	"context"
	"io"
	"time"
)

// NodeRole represents the role of a node in the cluster.
type NodeRole string

const (
	// NodeRoleHot is for frequently accessed data (fast storage).
	NodeRoleHot NodeRole = "hot"

	// NodeRoleWarm is for moderately accessed data.
	NodeRoleWarm NodeRole = "warm"

	// NodeRoleCold is for rarely accessed data (archival storage).
	NodeRoleCold NodeRole = "cold"
)

// NodeStatus represents the health status of a node.
type NodeStatus string

const (
	// NodeStatusHealthy indicates the node is functioning normally.
	NodeStatusHealthy NodeStatus = "healthy"

	// NodeStatusDegraded indicates the node has issues but is operational.
	NodeStatusDegraded NodeStatus = "degraded"

	// NodeStatusUnhealthy indicates the node is not functioning.
	NodeStatusUnhealthy NodeStatus = "unhealthy"

	// NodeStatusUnknown indicates the node status cannot be determined.
	NodeStatusUnknown NodeStatus = "unknown"
)

// Node represents a node in the Alexander Storage cluster.
type Node struct {
	// ID is the unique identifier for this node.
	ID string `json:"id"`

	// Address is the gRPC address (host:port) of this node.
	Address string `json:"address"`

	// Role indicates the storage tier of this node.
	Role NodeRole `json:"role"`

	// Status indicates the health of this node.
	Status NodeStatus `json:"status"`

	// LastHeartbeat is the last time a heartbeat was received.
	LastHeartbeat time.Time `json:"last_heartbeat"`

	// Stats contains storage utilization statistics.
	Stats *StorageStats `json:"stats,omitempty"`
}

// StorageStats contains storage utilization information for a node.
type StorageStats struct {
	// TotalBytes is the total storage capacity.
	TotalBytes int64 `json:"total_bytes"`

	// UsedBytes is the currently used storage.
	UsedBytes int64 `json:"used_bytes"`

	// FreeBytes is the available storage.
	FreeBytes int64 `json:"free_bytes"`

	// BlobCount is the number of blobs stored.
	BlobCount int64 `json:"blob_count"`
}

// BlobLocation tracks where a blob is stored in the cluster.
type BlobLocation struct {
	// ContentHash is the blob identifier.
	ContentHash string `json:"content_hash"`

	// NodeID is the node storing this blob.
	NodeID string `json:"node_id"`

	// IsPrimary indicates if this is the primary copy.
	IsPrimary bool `json:"is_primary"`

	// SyncedAt is when the blob was synced to this location.
	SyncedAt time.Time `json:"synced_at"`
}

// NodeClient provides methods for communicating with a remote node.
type NodeClient interface {
	// Ping checks if the node is alive.
	Ping(ctx context.Context) (*Node, error)

	// TransferBlob transfers a blob to this node.
	TransferBlob(ctx context.Context, contentHash string, size int64, reader io.Reader) error

	// RetrieveBlob retrieves a blob from this node.
	RetrieveBlob(ctx context.Context, contentHash string) (io.ReadCloser, error)

	// RetrieveBlobRange retrieves a range of bytes from a blob.
	RetrieveBlobRange(ctx context.Context, contentHash string, offset, length int64) (io.ReadCloser, error)

	// DeleteBlob deletes a blob from this node.
	DeleteBlob(ctx context.Context, contentHash string) error

	// BlobExists checks if a blob exists on this node.
	BlobExists(ctx context.Context, contentHash string) (bool, error)

	// Close closes the client connection.
	Close() error
}

// ClusterManager manages the cluster topology and blob locations.
type ClusterManager interface {
	// RegisterSelf registers this node with the cluster.
	RegisterSelf(ctx context.Context) error

	// SendHeartbeat sends a heartbeat to the cluster.
	SendHeartbeat(ctx context.Context) error

	// GetNodes returns all known nodes in the cluster.
	GetNodes(ctx context.Context) ([]*Node, error)

	// GetNode returns a specific node by ID.
	GetNode(ctx context.Context, nodeID string) (*Node, error)

	// GetNodesByRole returns all nodes with the specified role.
	GetNodesByRole(ctx context.Context, role NodeRole) ([]*Node, error)

	// GetHealthyNodes returns all healthy nodes.
	GetHealthyNodes(ctx context.Context) ([]*Node, error)

	// GetBlobLocations returns all locations for a blob.
	GetBlobLocations(ctx context.Context, contentHash string) ([]*BlobLocation, error)

	// RegisterBlobLocation registers a blob location.
	RegisterBlobLocation(ctx context.Context, location *BlobLocation) error

	// RemoveBlobLocation removes a blob location.
	RemoveBlobLocation(ctx context.Context, contentHash, nodeID string) error

	// GetClientForNode returns a client for communicating with a node.
	GetClientForNode(ctx context.Context, nodeID string) (NodeClient, error)

	// Close shuts down the cluster manager.
	Close() error
}

// NodeSelector provides strategies for selecting nodes for operations.
type NodeSelector interface {
	// SelectForStore selects nodes for storing a new blob.
	// Returns nodes in priority order.
	SelectForStore(ctx context.Context, size int64, replicationFactor int) ([]*Node, error)

	// SelectForRetrieve selects the best node for retrieving a blob.
	SelectForRetrieve(ctx context.Context, contentHash string) (*Node, error)

	// SelectForTiering selects a target node for tiering a blob.
	SelectForTiering(ctx context.Context, contentHash string, targetRole NodeRole) (*Node, error)
}

// ReplicationController manages blob replication across nodes.
type ReplicationController interface {
	// EnsureReplication ensures a blob has the desired replication factor.
	EnsureReplication(ctx context.Context, contentHash string, factor int) error

	// ReplicateTo replicates a blob to a specific node.
	ReplicateTo(ctx context.Context, contentHash string, targetNodeID string) error

	// RemoveReplica removes a blob replica from a node.
	RemoveReplica(ctx context.Context, contentHash string, nodeID string) error

	// GetReplicationStatus returns the replication status of a blob.
	GetReplicationStatus(ctx context.Context, contentHash string) (*ReplicationStatus, error)
}

// ReplicationStatus contains information about blob replication.
type ReplicationStatus struct {
	// ContentHash is the blob identifier.
	ContentHash string `json:"content_hash"`

	// ReplicaCount is the current number of replicas.
	ReplicaCount int `json:"replica_count"`

	// DesiredCount is the desired number of replicas.
	DesiredCount int `json:"desired_count"`

	// Locations are the nodes storing replicas.
	Locations []*BlobLocation `json:"locations"`

	// IsSufficient indicates if replication is at desired level.
	IsSufficient bool `json:"is_sufficient"`
}
