-- Canonical workflow rows are reconciled directly; delivery persistence is no
-- longer part of the store. Keep migration 0025 immutable and drop only its
-- delivery tables and indexes in this forward migration.
DROP TABLE workflow_event_outbox;
DROP TABLE project_event_sequences;
DROP TABLE watch_delivery_cursors;
