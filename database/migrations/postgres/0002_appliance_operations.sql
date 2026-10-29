-- Whole-Appliance operations (contract appliance-ops/v1alpha1).
--
-- One row per operation an administrator planned. OAM owns a row until the
-- operation is submitted to the host adapter; from then on the host's journal
-- is authoritative and this row follows it.
--
-- Nothing secret is stored: no passwords, challenges, archive contents or key
-- material. `plan` holds only what the host adapter returned when it
-- validated the request.

CREATE TABLE appliance_operations (
    -- UUIDv7, minted by OAM; also the host adapter's job ID. Time-ordered, so
    -- ordering by id is ordering by creation.
    id               UUID        PRIMARY KEY,
    type             TEXT        NOT NULL
        CHECK (type IN ('backup', 'restore', 'update', 'rollback', 'reset')),
    state            TEXT        NOT NULL
        CHECK (state IN ('PLANNED', 'AWAITING_AUTHORIZATION', 'QUEUED', 'RUNNING',
                         'VERIFYING', 'SUCCEEDED', 'FAILED', 'COMPENSATING',
                         'ROLLED_BACK', 'RECOVERY_REQUIRED', 'CANCELLED')),
    phase            TEXT        NOT NULL DEFAULT '',
    installation_id  TEXT        NOT NULL,
    model            TEXT        NOT NULL DEFAULT '',

    -- Who asked. The username is copied because the audit value of the row
    -- must survive the user being renamed or deleted; hence no foreign key.
    actor_user_id    INTEGER     NOT NULL,
    actor_username   TEXT        NOT NULL,
    request_id       TEXT        NOT NULL DEFAULT '',

    -- Idempotency: the same key with the same request is the same operation;
    -- the same key with a different request is a conflict.
    idempotency_key  TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    request          JSONB       NOT NULL,

    plan_hash        TEXT        NOT NULL,
    plan_expires_at  TIMESTAMPTZ NOT NULL,
    plan             JSONB       NOT NULL,

    -- Last host journal generation applied to this row; a lower one never
    -- overwrites a higher one.
    host_generation  BIGINT      NOT NULL DEFAULT 0,
    reconciliation   TEXT        NOT NULL DEFAULT 'IN_SYNC'
        CHECK (reconciliation IN ('IN_SYNC', 'RECOVERED_FROM_HOST', 'HOST_UNKNOWN', 'HOST_UNREACHABLE')),

    error_code       TEXT        NOT NULL DEFAULT '',
    error_origin     TEXT        NOT NULL DEFAULT ''
        CHECK (error_origin IN ('', 'oam', 'host', 'gateway')),

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    submitted_at     TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,

    CONSTRAINT uk_appliance_operations_idempotency UNIQUE (actor_user_id, idempotency_key)
);

CREATE INDEX idx_appliance_operations_state ON appliance_operations (state);

CREATE TRIGGER trg_appliance_operations_updated_at
    BEFORE UPDATE ON appliance_operations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
