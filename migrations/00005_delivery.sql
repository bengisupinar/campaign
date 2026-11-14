-- Delivery context: a dispatched campaign becomes one message per recipient and channel, sent
-- by the worker through the channel's provider. Campaigns gain the times and statuses of
-- dispatch. River's own job tables are migrated by River's migrator, not here (DECISIONS.md).

-- +goose Up
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns
    ADD CONSTRAINT campaigns_status_check CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'failed')),
    ADD COLUMN scheduled_at timestamptz, -- when a scheduled campaign is due
    ADD COLUMN started_at timestamptz,   -- when its dispatch began: the end of its segment's windows
    ADD COLUMN finished_at timestamptz,  -- when every message reached an outcome, or the dispatch failed
    ADD CONSTRAINT campaigns_scheduled_at_check CHECK (status <> 'scheduled' OR scheduled_at IS NOT NULL),
    ADD CONSTRAINT campaigns_started_at_check CHECK (status NOT IN ('sending', 'sent') OR started_at IS NOT NULL),
    ADD CONSTRAINT campaigns_finished_at_check CHECK (status NOT IN ('sent', 'failed') OR finished_at IS NOT NULL);

-- One message: a campaign to a customer on a channel. The unique key makes preparing a
-- campaign idempotent, so a retried dispatch can never message anyone twice.
CREATE TABLE deliveries (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id         bigint NOT NULL REFERENCES campaigns (id),
    customer_id         bigint NOT NULL REFERENCES customers (id),
    channel             text NOT NULL CHECK (channel IN ('email', 'sms')),
    status              text NOT NULL CHECK (status IN ('pending', 'accepted', 'delivered', 'failed', 'skipped')),
    provider_message_id text UNIQUE,    -- set when the provider accepts the message
    error               text CHECK (char_length(error) BETWEEN 1 AND 40), -- a reason code, never personal data
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    UNIQUE (campaign_id, customer_id, channel),
    CONSTRAINT deliveries_accepted_check CHECK (status NOT IN ('accepted', 'delivered') OR provider_message_id IS NOT NULL),
    CONSTRAINT deliveries_reason_check CHECK (status NOT IN ('failed', 'skipped') OR error IS NOT NULL),
    CONSTRAINT deliveries_updated_at_check CHECK (updated_at >= created_at)
);

-- Progress snapshots count by status and channel; completion asks whether anything is pending.
CREATE INDEX deliveries_campaign_status_idx ON deliveries (campaign_id, status, channel);

-- The mock providers' own records, standing in for a real provider's database: the first answer
-- to each idempotency key, so a repeated request is answered the same way, even after a restart.
-- Drop it when real providers replace the mocks.
CREATE TABLE mock_provider_messages (
    idempotency_key     text PRIMARY KEY,
    channel             text NOT NULL CHECK (channel IN ('email', 'sms')),
    outcome             text CHECK (outcome IN ('accepted', 'rejected')), -- NULL until answered
    provider_message_id text UNIQUE,
    attempts            integer NOT NULL CHECK (attempts >= 1),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT mock_provider_messages_accepted_check CHECK ((outcome = 'accepted') = (provider_message_id IS NOT NULL))
);

-- +goose Down
DROP TABLE mock_provider_messages;
DROP TABLE deliveries;
ALTER TABLE campaigns
    DROP CONSTRAINT campaigns_finished_at_check,
    DROP CONSTRAINT campaigns_started_at_check,
    DROP CONSTRAINT campaigns_scheduled_at_check,
    DROP COLUMN finished_at,
    DROP COLUMN started_at,
    DROP COLUMN scheduled_at,
    DROP CONSTRAINT campaigns_status_check,
    ADD CONSTRAINT campaigns_status_check CHECK (status IN ('draft', 'scheduled', 'sending'));
