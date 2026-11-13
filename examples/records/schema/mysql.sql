-- Operator step: apply once to a dedicated example database, never at app startup.
CREATE TABLE frame_records (
    id BIGINT PRIMARY KEY,
    title VARCHAR(200) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    created_by_kind VARCHAR(16) NOT NULL,
    created_by_id VARCHAR(128) NOT NULL,
    updated_by_kind VARCHAR(16) NOT NULL,
    updated_by_id VARCHAR(128) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE frame_record_audit (
    record_id BIGINT PRIMARY KEY,
    action VARCHAR(16) NOT NULL,
    actor_kind VARCHAR(16) NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    occurred_at DATETIME(3) NOT NULL,
    FOREIGN KEY (record_id) REFERENCES frame_records(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
