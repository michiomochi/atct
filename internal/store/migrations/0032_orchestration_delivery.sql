CREATE TABLE orchestration_delivery_leases (
    scope_key         TEXT NOT NULL REFERENCES orchestration_scope(scope_key),
    target_role       TEXT NOT NULL,
    holder_monitor_id TEXT NOT NULL,
    fencing_token     INTEGER NOT NULL CHECK (fencing_token > 0),
    expires_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (scope_key, target_role)
);

CREATE INDEX orchestration_delivery_leases_holder_idx
    ON orchestration_delivery_leases(holder_monitor_id);

CREATE TABLE orchestration_delivery_receipts (
    scope_key         TEXT NOT NULL REFERENCES orchestration_scope(scope_key),
    delivery_key      TEXT NOT NULL,
    generation        TEXT NOT NULL,
    target_role       TEXT NOT NULL,
    holder_monitor_id TEXT NOT NULL,
    fencing_token     INTEGER NOT NULL CHECK (fencing_token > 0),
    status            TEXT NOT NULL CHECK (status IN ('reserved', 'accepted', 'unknown')),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (scope_key, delivery_key, generation)
);

CREATE INDEX orchestration_delivery_receipts_status_idx
    ON orchestration_delivery_receipts(status, updated_at);
