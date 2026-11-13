-- PostgreSQL only. Apply explicitly using your service's migration tooling.
-- This example does not create tables at startup.
CREATE TABLE profiles (
    id varchar(128) PRIMARY KEY,
    display_name varchar(100) NOT NULL DEFAULT '',
    age integer NOT NULL DEFAULT 0 CHECK (age BETWEEN 0 AND 150),
    note varchar(1000)
);

-- A transactional snapshot history, not a general-purpose security audit log.
CREATE TABLE profile_changes (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile_id varchar(128) NOT NULL REFERENCES profiles(id),
    display_name varchar(100) NOT NULL,
    age integer NOT NULL,
    note varchar(1000),
    changed_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
