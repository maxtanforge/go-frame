CREATE TABLE products (
 id BIGINT PRIMARY KEY,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
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
);
CREATE TABLE product_events (
 id BIGINT PRIMARY KEY,
 created_at TIMESTAMPTZ(3) NOT NULL,
 created_by VARCHAR(128) NOT NULL,
 created_by_kind VARCHAR(16) NOT NULL,
 created_by_name VARCHAR(128) NOT NULL,
 product_id BIGINT NOT NULL,
 action VARCHAR(255) NOT NULL,
 CONSTRAINT product_events_action_enum CHECK (action IN ('created', 'updated', 'deleted'))
);
