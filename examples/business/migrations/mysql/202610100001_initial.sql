CREATE TABLE business_books (
    id BIGINT NOT NULL PRIMARY KEY CHECK (id > 0),
    owner VARCHAR(128) NOT NULL CHECK (CHAR_LENGTH(owner) > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE business_categories (
    id BIGINT NOT NULL PRIMARY KEY CHECK (id > 0),
    parent_id BIGINT NOT NULL,
    owner VARCHAR(128) NOT NULL CHECK (CHAR_LENGTH(owner) > 0),
    name VARCHAR(100) NOT NULL CHECK (OCTET_LENGTH(name) BETWEEN 1 AND 100),
    note VARCHAR(1000) NULL CHECK (note IS NULL OR OCTET_LENGTH(note) <= 1000),
    system BOOLEAN NOT NULL DEFAULT FALSE,
    CONSTRAINT business_categories_business_books_categories
        FOREIGN KEY (parent_id) REFERENCES business_books (id) ON DELETE NO ACTION,
    INDEX category_parent_id_owner_id (parent_id, owner, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
