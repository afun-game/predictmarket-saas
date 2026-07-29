-- Preserve the authoritative origin of every event result. Resolution and
-- audit insertion share one transaction so the payout outbox stays traceable.
-- +goose Up
CREATE TABLE IF NOT EXISTS event_resolution_audits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id),
    outcome VARCHAR(255) NOT NULL,
    resolution_source VARCHAR(50) NOT NULL
        CHECK (resolution_source IN ('manual', 'polymarket')),
    resolved_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_event_resolution_audits_event
    ON event_resolution_audits(event_id, resolved_at DESC);

-- +goose Down
DROP TABLE IF EXISTS event_resolution_audits;
