-- Remap legacy/free-form categories onto the new market category catalog:
-- hot, football, basketball, baseball, boxing, weather, bitcoin, other.
-- +goose Up

-- Sports events: map by league.
UPDATE events SET category = 'basketball' WHERE category = 'sports' AND id IN (SELECT event_id FROM sports_events WHERE league IN ('nba', 'wnba'));
UPDATE events SET category = 'baseball'   WHERE category = 'sports' AND id IN (SELECT event_id FROM sports_events WHERE league IN ('mlb', 'lmb'));
UPDATE events SET category = 'football'   WHERE category = 'sports' AND id IN (SELECT event_id FROM sports_events WHERE league = 'epl');
UPDATE events SET category = 'boxing'     WHERE category = 'sports' AND id IN (SELECT event_id FROM sports_events WHERE league = 'boxing');
UPDATE events SET category = 'other'      WHERE category = 'sports';

-- Crypto family.
UPDATE events SET category = 'bitcoin' WHERE category IN ('crypto', 'ethereum');

-- Everything else that is not already a catalog key.
UPDATE events SET category = 'other'
WHERE category NOT IN ('hot', 'football', 'basketball', 'baseball', 'boxing', 'weather', 'bitcoin', 'other');

-- Markets follow their owning event's category (they inherit it at creation).
UPDATE markets m SET category = e.category
FROM events e
WHERE e.id = m.event_id AND m.category IS DISTINCT FROM e.category;

-- Leftover empty/unknown market categories.
UPDATE markets SET category = 'other'
WHERE category = '' OR category IS NULL
   OR category NOT IN ('hot', 'football', 'basketball', 'baseball', 'boxing', 'weather', 'bitcoin', 'other');

-- +goose Down

-- The previous free-form categories cannot be reconstructed reliably; this
-- migration is not reversible. Down is intentionally a no-op.
