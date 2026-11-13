-- Operator step: apply once to a dedicated example database, never at app startup.
CREATE TABLE frame_records (
    id BIGINT PRIMARY KEY,
    title VARCHAR(200) NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    created_by_kind VARCHAR(16) NOT NULL,
    created_by_id VARCHAR(128) NOT NULL,
    updated_by_kind VARCHAR(16) NOT NULL,
    updated_by_id VARCHAR(128) NOT NULL
);

CREATE TABLE frame_record_audit (
    record_id BIGINT PRIMARY KEY REFERENCES frame_records(id),
    action VARCHAR(16) NOT NULL,
    actor_kind VARCHAR(16) NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    occurred_at TIMESTAMPTZ(3) NOT NULL
);
