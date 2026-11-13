-- Example-owned PostgreSQL schema. Apply explicitly; never on service startup.
CREATE TABLE device_events (
    id VARCHAR(128) PRIMARY KEY,
    user_id VARCHAR(128) NOT NULL,
    steps BIGINT NOT NULL CHECK (steps >= 0),
    delivered BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX device_events_pending ON device_events (id) WHERE delivered = FALSE;
