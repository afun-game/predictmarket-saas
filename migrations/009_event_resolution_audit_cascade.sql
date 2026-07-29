-- +goose Up
-- Test fixtures and controlled data-retention cleanup may remove an event;
-- its audit trail is not independently meaningful without the event.
ALTER TABLE event_resolution_audits
    DROP CONSTRAINT IF EXISTS event_resolution_audits_event_id_fkey;

ALTER TABLE event_resolution_audits
    ADD CONSTRAINT event_resolution_audits_event_id_fkey
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE event_resolution_audits
    DROP CONSTRAINT IF EXISTS event_resolution_audits_event_id_fkey;

ALTER TABLE event_resolution_audits
    ADD CONSTRAINT event_resolution_audits_event_id_fkey
    FOREIGN KEY (event_id) REFERENCES events(id);
