package repository

// deviceSchemaV7 snapshots go-server-common/devicegrant schema version 1.
// Never edit a released migration; apply future shared schema changes separately.
const deviceSchemaV7 = `CREATE TABLE device_authorizations (
 device_code_hash BLOB PRIMARY KEY NOT NULL,
 user_code TEXT UNIQUE NOT NULL,
 client_id TEXT NOT NULL,
 name TEXT NOT NULL,
 requested_scope TEXT NOT NULL,
 requested_lifetime TEXT NOT NULL,
 requester TEXT NOT NULL,
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 last_poll_at TEXT NOT NULL DEFAULT '',
 interval INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','denied')),
 granted_scope TEXT NOT NULL DEFAULT '',
 granted_expiry TEXT NOT NULL DEFAULT ''
)`

// Invalidate consent on deletion even if the app later reuses the resource ID.
const deviceGrantScopeDeleteTrigger = `CREATE TRIGGER device_authorizations_resource_deleted
AFTER DELETE ON folders BEGIN
 DELETE FROM device_authorizations
 WHERE status='approved'
 AND instr(' ' || granted_scope || ' ', ' folder:' || OLD.id || ' ') > 0;
END`
