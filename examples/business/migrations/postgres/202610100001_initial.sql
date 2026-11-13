CREATE TABLE business_books (
    id BIGINT PRIMARY KEY CHECK (id > 0),
    owner VARCHAR(128) NOT NULL CHECK (length(owner) > 0)
);
CREATE TABLE business_categories (
    id BIGINT PRIMARY KEY CHECK (id > 0),
    parent_id BIGINT NOT NULL,
    owner VARCHAR(128) NOT NULL CHECK (length(owner) > 0),
    name VARCHAR(100) NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 100),
    note VARCHAR(1000) NULL CHECK (note IS NULL OR octet_length(note) <= 1000),
    system BOOLEAN NOT NULL DEFAULT FALSE,
    CONSTRAINT business_categories_business_books_categories
        FOREIGN KEY (parent_id) REFERENCES business_books (id) ON DELETE NO ACTION
);
CREATE INDEX category_parent_id_owner_id ON business_categories (parent_id, owner, id);
