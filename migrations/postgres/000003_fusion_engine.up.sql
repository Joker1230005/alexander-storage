-- Alexander Storage v2.0 - Fusion Engine Migration
-- Adds support for composite blobs, delta versioning, CDC chunking, and cluster management

-- ============================================================================
-- BLOB ENHANCEMENTS
-- ============================================================================

-- Add blob_type column to blobs table
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS blob_type VARCHAR(20) DEFAULT 'single';
COMMENT ON COLUMN blobs.blob_type IS 'Type of blob: single, composite, or delta';

-- Add encryption_scheme column
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS encryption_scheme VARCHAR(50);
COMMENT ON COLUMN blobs.encryption_scheme IS 'Encryption algorithm: aes-256-gcm, chacha20-poly1305-stream';

-- Add delta_base_hash for delta blobs
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS delta_base_hash VARCHAR(64);
COMMENT ON COLUMN blobs.delta_base_hash IS 'Base blob hash for delta blobs';

-- Add index on blob_type for efficient filtering
CREATE INDEX IF NOT EXISTS idx_blobs_blob_type ON blobs(blob_type);

-- Add index on delta_base_hash for delta chain traversal
CREATE INDEX IF NOT EXISTS idx_blobs_delta_base_hash ON blobs(delta_base_hash) WHERE delta_base_hash IS NOT NULL;

-- ============================================================================
-- COMPOSITE BLOB PARTS
-- ============================================================================

-- Table for storing part references in composite blobs
CREATE TABLE IF NOT EXISTS blob_parts (
    composite_hash VARCHAR(64) NOT NULL,
    part_index INTEGER NOT NULL,
    part_hash VARCHAR(64) NOT NULL,
    part_offset BIGINT NOT NULL,
    part_size BIGINT NOT NULL,
    PRIMARY KEY (composite_hash, part_index),
    CONSTRAINT fk_blob_parts_composite FOREIGN KEY (composite_hash) REFERENCES blobs(content_hash) ON DELETE CASCADE,
    CONSTRAINT fk_blob_parts_part FOREIGN KEY (part_hash) REFERENCES blobs(content_hash) ON DELETE RESTRICT
);

COMMENT ON TABLE blob_parts IS 'Stores part references for composite blobs (multipart uploads without concatenation)';

CREATE INDEX IF NOT EXISTS idx_blob_parts_part_hash ON blob_parts(part_hash);

-- ============================================================================
-- DELTA INSTRUCTIONS
-- ============================================================================

-- Table for storing delta instructions
CREATE TABLE IF NOT EXISTS blob_deltas (
    delta_hash VARCHAR(64) PRIMARY KEY,
    base_hash VARCHAR(64) NOT NULL,
    instruction_count INTEGER NOT NULL,
    delta_data_size BIGINT NOT NULL,
    total_size BIGINT NOT NULL,
    savings_ratio DECIMAL(5,4),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT fk_blob_deltas_delta FOREIGN KEY (delta_hash) REFERENCES blobs(content_hash) ON DELETE CASCADE,
    CONSTRAINT fk_blob_deltas_base FOREIGN KEY (base_hash) REFERENCES blobs(content_hash) ON DELETE RESTRICT
);

COMMENT ON TABLE blob_deltas IS 'Stores delta metadata for delta versioning';

-- Table for individual delta instructions
CREATE TABLE IF NOT EXISTS delta_instructions (
    id BIGSERIAL PRIMARY KEY,
    delta_hash VARCHAR(64) NOT NULL,
    instruction_index INTEGER NOT NULL,
    instruction_type VARCHAR(10) NOT NULL, -- 'copy' or 'insert'
    source_offset BIGINT NOT NULL,
    target_offset BIGINT NOT NULL,
    length BIGINT NOT NULL,
    CONSTRAINT fk_delta_instructions_delta FOREIGN KEY (delta_hash) REFERENCES blob_deltas(delta_hash) ON DELETE CASCADE,
    UNIQUE (delta_hash, instruction_index)
);

COMMENT ON TABLE delta_instructions IS 'Stores individual copy/insert instructions for delta reconstruction';

CREATE INDEX IF NOT EXISTS idx_delta_instructions_delta_hash ON delta_instructions(delta_hash);

-- ============================================================================
-- CDC CHUNKS (Content-Defined Chunking)
-- ============================================================================

-- Table for storing CDC chunks for sub-file deduplication
CREATE TABLE IF NOT EXISTS cdc_chunks (
    chunk_hash VARCHAR(64) PRIMARY KEY,
    chunk_size INTEGER NOT NULL,
    ref_count INTEGER DEFAULT 1,
    storage_path VARCHAR(512),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

COMMENT ON TABLE cdc_chunks IS 'Stores content-defined chunks for sub-file deduplication';

CREATE INDEX IF NOT EXISTS idx_cdc_chunks_ref_count ON cdc_chunks(ref_count) WHERE ref_count = 0;

-- Table for mapping blobs to their CDC chunks
CREATE TABLE IF NOT EXISTS blob_chunks (
    blob_hash VARCHAR(64) NOT NULL,
    chunk_index INTEGER NOT NULL,
    chunk_hash VARCHAR(64) NOT NULL,
    chunk_offset BIGINT NOT NULL,
    PRIMARY KEY (blob_hash, chunk_index),
    CONSTRAINT fk_blob_chunks_blob FOREIGN KEY (blob_hash) REFERENCES blobs(content_hash) ON DELETE CASCADE,
    CONSTRAINT fk_blob_chunks_chunk FOREIGN KEY (chunk_hash) REFERENCES cdc_chunks(chunk_hash) ON DELETE RESTRICT
);

COMMENT ON TABLE blob_chunks IS 'Maps blobs to their CDC chunks for reconstruction';

CREATE INDEX IF NOT EXISTS idx_blob_chunks_chunk_hash ON blob_chunks(chunk_hash);

-- ============================================================================
-- CLUSTER MANAGEMENT
-- ============================================================================

-- Table for cluster node registry
CREATE TABLE IF NOT EXISTS cluster_nodes (
    node_id VARCHAR(64) PRIMARY KEY,
    address VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL, -- 'hot', 'warm', 'cold'
    status VARCHAR(20) DEFAULT 'unknown', -- 'healthy', 'degraded', 'unhealthy', 'unknown'
    total_bytes BIGINT DEFAULT 0,
    used_bytes BIGINT DEFAULT 0,
    free_bytes BIGINT DEFAULT 0,
    blob_count BIGINT DEFAULT 0,
    last_heartbeat TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

COMMENT ON TABLE cluster_nodes IS 'Registry of nodes in the Alexander Storage cluster';

CREATE INDEX IF NOT EXISTS idx_cluster_nodes_role ON cluster_nodes(role);
CREATE INDEX IF NOT EXISTS idx_cluster_nodes_status ON cluster_nodes(status);

-- Table for tracking blob locations across nodes
CREATE TABLE IF NOT EXISTS blob_locations (
    content_hash VARCHAR(64) NOT NULL,
    node_id VARCHAR(64) NOT NULL,
    is_primary BOOLEAN DEFAULT FALSE,
    synced_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (content_hash, node_id),
    CONSTRAINT fk_blob_locations_blob FOREIGN KEY (content_hash) REFERENCES blobs(content_hash) ON DELETE CASCADE,
    CONSTRAINT fk_blob_locations_node FOREIGN KEY (node_id) REFERENCES cluster_nodes(node_id) ON DELETE CASCADE
);

COMMENT ON TABLE blob_locations IS 'Tracks which nodes store which blobs';

CREATE INDEX IF NOT EXISTS idx_blob_locations_node ON blob_locations(node_id);
CREATE INDEX IF NOT EXISTS idx_blob_locations_primary ON blob_locations(content_hash) WHERE is_primary = TRUE;

-- ============================================================================
-- ACCESS TRACKING (for tiering decisions)
-- ============================================================================

-- Table for tracking blob access patterns
CREATE TABLE IF NOT EXISTS blob_access_log (
    id BIGSERIAL PRIMARY KEY,
    content_hash VARCHAR(64) NOT NULL,
    accessed_at TIMESTAMPTZ DEFAULT NOW(),
    access_type VARCHAR(20) DEFAULT 'read', -- 'read', 'write', 'head'
    CONSTRAINT fk_blob_access_log_blob FOREIGN KEY (content_hash) REFERENCES blobs(content_hash) ON DELETE CASCADE
);

COMMENT ON TABLE blob_access_log IS 'Logs blob accesses for tiering decisions';

CREATE INDEX IF NOT EXISTS idx_blob_access_log_hash_time ON blob_access_log(content_hash, accessed_at DESC);
CREATE INDEX IF NOT EXISTS idx_blob_access_log_time ON blob_access_log(accessed_at);

-- Table for aggregated access statistics
CREATE TABLE IF NOT EXISTS blob_access_stats (
    content_hash VARCHAR(64) PRIMARY KEY,
    total_access_count INTEGER DEFAULT 0,
    last_access_time TIMESTAMPTZ,
    first_access_time TIMESTAMPTZ,
    accesses_last_24h INTEGER DEFAULT 0,
    accesses_last_7d INTEGER DEFAULT 0,
    accesses_last_30d INTEGER DEFAULT 0,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT fk_blob_access_stats_blob FOREIGN KEY (content_hash) REFERENCES blobs(content_hash) ON DELETE CASCADE
);

COMMENT ON TABLE blob_access_stats IS 'Aggregated access statistics for tiering decisions';

-- ============================================================================
-- MIGRATION TRACKING
-- ============================================================================

-- Table for tracking migration progress
CREATE TABLE IF NOT EXISTS migration_progress (
    migration_type VARCHAR(50) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL, -- 'pending', 'in_progress', 'completed', 'failed', 'skipped'
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    PRIMARY KEY (migration_type, content_hash)
);

COMMENT ON TABLE migration_progress IS 'Tracks migration progress for background migration worker';

CREATE INDEX IF NOT EXISTS idx_migration_progress_status ON migration_progress(migration_type, status);
CREATE INDEX IF NOT EXISTS idx_migration_progress_pending ON migration_progress(migration_type) WHERE status = 'pending';

-- ============================================================================
-- FUNCTIONS
-- ============================================================================

-- Function to update access statistics after each access
CREATE OR REPLACE FUNCTION update_blob_access_stats()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO blob_access_stats (content_hash, total_access_count, last_access_time, first_access_time, updated_at)
    VALUES (NEW.content_hash, 1, NEW.accessed_at, NEW.accessed_at, NOW())
    ON CONFLICT (content_hash) DO UPDATE SET
        total_access_count = blob_access_stats.total_access_count + 1,
        last_access_time = NEW.accessed_at,
        updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger to update access stats
DROP TRIGGER IF EXISTS trg_update_blob_access_stats ON blob_access_log;
CREATE TRIGGER trg_update_blob_access_stats
    AFTER INSERT ON blob_access_log
    FOR EACH ROW
    EXECUTE FUNCTION update_blob_access_stats();

-- Function to increment CDC chunk ref count
CREATE OR REPLACE FUNCTION increment_chunk_ref(p_chunk_hash VARCHAR(64))
RETURNS void AS $$
BEGIN
    UPDATE cdc_chunks SET ref_count = ref_count + 1 WHERE chunk_hash = p_chunk_hash;
END;
$$ LANGUAGE plpgsql;

-- Function to decrement CDC chunk ref count
CREATE OR REPLACE FUNCTION decrement_chunk_ref(p_chunk_hash VARCHAR(64))
RETURNS INTEGER AS $$
DECLARE
    new_count INTEGER;
BEGIN
    UPDATE cdc_chunks SET ref_count = ref_count - 1 WHERE chunk_hash = p_chunk_hash
    RETURNING ref_count INTO new_count;
    RETURN COALESCE(new_count, 0);
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- VIEWS
-- ============================================================================

-- View for cluster health overview
CREATE OR REPLACE VIEW cluster_health AS
SELECT 
    role,
    COUNT(*) as node_count,
    SUM(CASE WHEN status = 'healthy' THEN 1 ELSE 0 END) as healthy_nodes,
    SUM(CASE WHEN status = 'degraded' THEN 1 ELSE 0 END) as degraded_nodes,
    SUM(CASE WHEN status = 'unhealthy' THEN 1 ELSE 0 END) as unhealthy_nodes,
    SUM(total_bytes) as total_capacity,
    SUM(used_bytes) as total_used,
    SUM(free_bytes) as total_free,
    SUM(blob_count) as total_blobs
FROM cluster_nodes
GROUP BY role;

-- View for migration overview
CREATE OR REPLACE VIEW migration_overview AS
SELECT 
    migration_type,
    COUNT(*) as total_blobs,
    SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) as pending,
    SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END) as in_progress,
    SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) as completed,
    SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed,
    SUM(CASE WHEN status = 'skipped' THEN 1 ELSE 0 END) as skipped,
    ROUND(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END)::DECIMAL / NULLIF(COUNT(*), 0) * 100, 2) as progress_percent
FROM migration_progress
GROUP BY migration_type;
