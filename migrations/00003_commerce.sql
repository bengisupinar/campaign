-- Commerce context: what customers buy or intend to buy. Amounts are in kuruş (TRY).
-- Rules that span rows (e.g. an order has at least one line) live in internal/commerce.

-- +goose Up
CREATE TABLE categories (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE
);

CREATE TABLE products (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    category_id bigint NOT NULL REFERENCES categories (id),
    name        text NOT NULL UNIQUE,
    price_minor bigint NOT NULL CHECK (price_minor > 0)
);

CREATE INDEX products_category_id_idx ON products (category_id);

-- A customer's open basket: one line per product. There is no basket table; "abandoned" is
-- derived from added_at and later orders.
CREATE TABLE basket_items (
    customer_id bigint NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    product_id  bigint NOT NULL REFERENCES products (id),
    quantity    integer NOT NULL CHECK (quantity > 0),
    added_at    timestamptz NOT NULL,
    PRIMARY KEY (customer_id, product_id)
);

CREATE INDEX basket_items_product_id_idx ON basket_items (product_id);

-- Only completed orders count as purchases; cancelled and refunded are final states.
CREATE TABLE orders (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id       bigint NOT NULL REFERENCES customers (id),
    status            text NOT NULL CHECK (status IN ('completed', 'cancelled', 'refunded')),
    ordered_at        timestamptz NOT NULL,
    status_changed_at timestamptz NOT NULL, -- when the current status began
    CONSTRAINT orders_status_changed_at_check CHECK (status_changed_at >= ordered_at)
);

CREATE INDEX orders_customer_id_ordered_at_idx ON orders (customer_id, ordered_at);

-- Order lines record the price paid, so later price changes never rewrite history.
CREATE TABLE order_items (
    order_id         bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id       bigint NOT NULL REFERENCES products (id),
    quantity         integer NOT NULL CHECK (quantity > 0),
    unit_price_minor bigint NOT NULL CHECK (unit_price_minor >= 0),
    PRIMARY KEY (order_id, product_id)
);

CREATE INDEX order_items_product_id_idx ON order_items (product_id);

-- +goose Down
DROP TABLE order_items;
DROP TABLE orders;
DROP TABLE basket_items;
DROP TABLE products;
DROP TABLE categories;
