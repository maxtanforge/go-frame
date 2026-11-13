CREATE TABLE products (
 id BIGINT PRIMARY KEY,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 created_by VARCHAR(128) NOT NULL,
 updated_by VARCHAR(128) NOT NULL,
 created_by_kind VARCHAR(16) NOT NULL,
 created_by_name VARCHAR(128) NOT NULL,
 updated_by_kind VARCHAR(16) NOT NULL,
 updated_by_name VARCHAR(128) NOT NULL,
 name VARCHAR(200) NOT NULL,
 price BIGINT NOT NULL,
 active BOOLEAN NOT NULL,
 note VARCHAR(1000) NULL,
 CONSTRAINT products_price_nonnegative CHECK (price >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE product_events (
 id BIGINT PRIMARY KEY,
 created_at DATETIME(3) NOT NULL,
 created_by VARCHAR(128) NOT NULL,
 created_by_kind VARCHAR(16) NOT NULL,
 created_by_name VARCHAR(128) NOT NULL,
 product_id BIGINT NOT NULL,
 action ENUM('created','updated','deleted') NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
