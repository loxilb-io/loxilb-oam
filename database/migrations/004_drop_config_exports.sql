-- Drops the legacy config_exports table. The /oam/config/* feature it backed
-- was removed in favor of instance snapshots (instance_snapshots, migration 003).
--
-- NOTE: Apply ONE RELEASE AFTER the release that removes the /oam/config/*
-- endpoints, so a rollback of that release still finds its table. The rows
-- only reference export files on ephemeral container storage, so no data of
-- value is lost.

USE loxioam;

DROP TABLE IF EXISTS config_exports;
