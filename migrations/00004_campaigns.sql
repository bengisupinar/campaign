-- Campaign context: a message to a segment of customers on SMS, email or both. Segment rules
-- are a JSON DSL (version 1) validated by internal/campaign and compiled into parameterised SQL
-- by the segmentation engine. Only drafts change; delivery will extend the statuses.

-- +goose Up
CREATE TABLE campaigns (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    status        text NOT NULL CHECK (status IN ('draft', 'scheduled', 'sending')),
    segment       jsonb NOT NULL CHECK (jsonb_typeof(segment) = 'object'),
    sms_body      text CHECK (char_length(sms_body) BETWEEN 1 AND 160), -- NULL: no SMS
    email_subject text CHECK (char_length(email_subject) BETWEEN 1 AND 80), -- NULL: no email
    email_body    text CHECK (char_length(email_body) >= 1),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    CONSTRAINT campaigns_channel_check CHECK (sms_body IS NOT NULL OR email_subject IS NOT NULL),
    CONSTRAINT campaigns_email_check CHECK ((email_subject IS NULL) = (email_body IS NULL)),
    CONSTRAINT campaigns_updated_at_check CHECK (updated_at >= created_at)
);

-- +goose Down
DROP TABLE campaigns;
