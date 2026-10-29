-- Which domains of the snapshot the last restore was limited to, as the
-- comma-separated list sent to the gateway. NULL means the whole document,
-- which is also what every restore before this column was.
ALTER TABLE instance_snapshots
    ADD COLUMN last_restore_components TEXT NULL;

COMMENT ON COLUMN instance_snapshots.last_restore_components IS 'Domains the last restore was limited to (comma-separated); NULL = whole document';
