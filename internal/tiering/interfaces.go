// Package tiering provides automatic data tiering for Alexander Storage.
// It moves blobs between storage tiers based on access patterns and policies.
package tiering

import (
	"context"
	"time"

	"github.com/prn-tf/alexander-storage/internal/cluster"
	"github.com/prn-tf/alexander-storage/internal/domain"
)

// Policy defines when and how to tier blobs.
type Policy struct {
	// Name is the unique identifier for this policy.
	Name string `json:"name" yaml:"name"`

	// Priority determines evaluation order (lower = higher priority).
	Priority int `json:"priority" yaml:"priority"`

	// Enabled indicates if the policy is active.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Condition defines when the policy applies.
	Condition Condition `json:"condition" yaml:"condition"`

	// Action defines what to do when condition is met.
	Action Action `json:"action" yaml:"action"`
}

// Condition defines the criteria for triggering a policy.
type Condition struct {
	// MinAge is the minimum age since creation (e.g., "30d", "720h").
	MinAge time.Duration `json:"min_age,omitempty" yaml:"min_age,omitempty"`

	// LastAccessedBefore triggers if last access was before this duration.
	LastAccessedBefore time.Duration `json:"last_accessed_before,omitempty" yaml:"last_accessed_before,omitempty"`

	// AccessCountBelow triggers if access count is below this threshold.
	AccessCountBelow *int `json:"access_count_below,omitempty" yaml:"access_count_below,omitempty"`

	// AccessCountAbove triggers if access count is above this threshold.
	AccessCountAbove *int `json:"access_count_above,omitempty" yaml:"access_count_above,omitempty"`

	// SizeAbove triggers for blobs larger than this size (bytes).
	SizeAbove *int64 `json:"size_above,omitempty" yaml:"size_above,omitempty"`

	// SizeBelow triggers for blobs smaller than this size (bytes).
	SizeBelow *int64 `json:"size_below,omitempty" yaml:"size_below,omitempty"`

	// CurrentTier triggers only for blobs in this tier.
	CurrentTier *cluster.NodeRole `json:"current_tier,omitempty" yaml:"current_tier,omitempty"`

	// BlobType triggers only for specific blob types.
	BlobType *domain.BlobType `json:"blob_type,omitempty" yaml:"blob_type,omitempty"`
}

// ActionType defines the type of tiering action.
type ActionType string

const (
	// ActionMoveTo moves the blob to a different tier.
	ActionMoveTo ActionType = "move_to"

	// ActionDelete deletes the blob.
	ActionDelete ActionType = "delete"

	// ActionCompress compresses the blob.
	ActionCompress ActionType = "compress"

	// ActionKeep keeps the blob in current tier (blocks other policies).
	ActionKeep ActionType = "keep"
)

// Action defines what to do when a policy condition is met.
type Action struct {
	// Type is the action type.
	Type ActionType `json:"type" yaml:"type"`

	// TargetTier is the destination tier for "move_to" actions.
	TargetTier *cluster.NodeRole `json:"target_tier,omitempty" yaml:"target_tier,omitempty"`

	// TargetNode is a specific node ID (optional, overrides tier selection).
	TargetNode *string `json:"target_node,omitempty" yaml:"target_node,omitempty"`

	// DeleteAfterMove deletes from source after successful move.
	DeleteAfterMove bool `json:"delete_after_move,omitempty" yaml:"delete_after_move,omitempty"`
}

// Decision represents the result of policy evaluation for a blob.
type Decision struct {
	// Blob is the blob being evaluated.
	Blob *domain.Blob `json:"blob"`

	// Policy is the policy that triggered this decision (nil if no action).
	Policy *Policy `json:"policy,omitempty"`

	// Action is the action to take.
	Action *Action `json:"action,omitempty"`

	// Reason is a human-readable explanation.
	Reason string `json:"reason"`

	// ShouldAct indicates if an action should be taken.
	ShouldAct bool `json:"should_act"`
}

// Controller evaluates policies and executes tiering decisions.
type Controller interface {
	// AddPolicy adds a tiering policy.
	AddPolicy(policy Policy) error

	// RemovePolicy removes a policy by name.
	RemovePolicy(name string) error

	// GetPolicies returns all configured policies.
	GetPolicies() []Policy

	// Evaluate evaluates policies for a single blob.
	Evaluate(ctx context.Context, blob *domain.Blob) (*Decision, error)

	// EvaluateAll evaluates policies for all blobs and returns decisions.
	// Only returns decisions where ShouldAct is true.
	EvaluateAll(ctx context.Context) ([]*Decision, error)

	// Execute executes a tiering decision.
	Execute(ctx context.Context, decision *Decision) error

	// RunOnce evaluates and executes all policies once.
	RunOnce(ctx context.Context) (*RunResult, error)

	// Start starts the background tiering controller.
	Start(ctx context.Context) error

	// Stop stops the background tiering controller.
	Stop() error
}

// RunResult contains the results of a tiering run.
type RunResult struct {
	// StartTime is when the run started.
	StartTime time.Time `json:"start_time"`

	// EndTime is when the run completed.
	EndTime time.Time `json:"end_time"`

	// Duration is how long the run took.
	Duration time.Duration `json:"duration"`

	// BlobsEvaluated is the number of blobs evaluated.
	BlobsEvaluated int `json:"blobs_evaluated"`

	// DecisionsMade is the number of decisions made.
	DecisionsMade int `json:"decisions_made"`

	// ActionsExecuted is the number of actions successfully executed.
	ActionsExecuted int `json:"actions_executed"`

	// ActionsFailed is the number of actions that failed.
	ActionsFailed int `json:"actions_failed"`

	// BytesMoved is the total bytes moved between tiers.
	BytesMoved int64 `json:"bytes_moved"`

	// Errors contains any errors encountered.
	Errors []string `json:"errors,omitempty"`
}

// BlobAccessTracker tracks blob access patterns for tiering decisions.
type BlobAccessTracker interface {
	// RecordAccess records an access to a blob.
	RecordAccess(ctx context.Context, contentHash string) error

	// GetAccessCount returns the access count for a blob.
	GetAccessCount(ctx context.Context, contentHash string) (int, error)

	// GetLastAccess returns the last access time for a blob.
	GetLastAccess(ctx context.Context, contentHash string) (time.Time, error)

	// GetAccessStats returns full access statistics for a blob.
	GetAccessStats(ctx context.Context, contentHash string) (*AccessStats, error)

	// Cleanup removes old access records.
	Cleanup(ctx context.Context, olderThan time.Duration) error
}

// AccessStats contains detailed access statistics for a blob.
type AccessStats struct {
	// ContentHash is the blob identifier.
	ContentHash string `json:"content_hash"`

	// TotalAccessCount is the total number of accesses.
	TotalAccessCount int `json:"total_access_count"`

	// LastAccessTime is the most recent access time.
	LastAccessTime time.Time `json:"last_access_time"`

	// FirstAccessTime is the first access time.
	FirstAccessTime time.Time `json:"first_access_time"`

	// AccessesLast24h is the number of accesses in the last 24 hours.
	AccessesLast24h int `json:"accesses_last_24h"`

	// AccessesLast7d is the number of accesses in the last 7 days.
	AccessesLast7d int `json:"accesses_last_7d"`

	// AccessesLast30d is the number of accesses in the last 30 days.
	AccessesLast30d int `json:"accesses_last_30d"`
}
