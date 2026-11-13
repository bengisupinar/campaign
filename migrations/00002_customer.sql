-- Customer context: where customers live, how to reach them and what they allow.
-- Rules that span rows (e.g. an SMS permission needs a phone) live in internal/customer.

-- +goose Up
CREATE TABLE countries (
    code char(2) PRIMARY KEY CHECK (code ~ '^[A-Z]{2}$'), -- ISO 3166-1 alpha-2, e.g. TR
    name text NOT NULL UNIQUE
);

CREATE TABLE cities (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    country_code char(2) NOT NULL REFERENCES countries (code),
    name         text NOT NULL,
    UNIQUE (country_code, name) -- also serves country filters through its leading column
);

CREATE TABLE customers (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    first_name         text NOT NULL,
    last_name          text NOT NULL,
    email              text NOT NULL UNIQUE, -- lowercased by the domain
    phone              text UNIQUE CHECK (phone ~ '^\+[1-9][0-9]{6,14}$'), -- E.164; optional
    city_id            bigint NOT NULL REFERENCES cities (id),
    preferred_language text NOT NULL CHECK (preferred_language IN ('tr', 'de', 'en')),
    created_at         timestamptz NOT NULL DEFAULT now() -- the signup date
);

CREATE INDEX customers_city_id_idx ON customers (city_id);

-- Marketing permission per channel. A missing row means "not permitted" (opt-in).
CREATE TABLE notification_permissions (
    customer_id bigint NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    channel     text NOT NULL CHECK (channel IN ('sms', 'email')),
    granted     boolean NOT NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (customer_id, channel)
);

-- +goose Down
DROP TABLE notification_permissions;
DROP TABLE customers;
DROP TABLE cities;
DROP TABLE countries;
