package apikit

import (
	"database/sql"
	"fmt"

	"github.com/CarlSinclair/csuit-libs/logger"
)

var sqliteStatements = []string{
	`CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  key_hash TEXT NOT NULL UNIQUE,
  key_prefix TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  scopes TEXT NOT NULL,
  rate_limit INTEGER NOT NULL DEFAULT 0,
  created_by INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  last_used_at TEXT NULL,
  last_used_ip TEXT NOT NULL DEFAULT '',
  expires_at TEXT NULL,
  revoked_at TEXT NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_api_keys_owner ON api_keys(created_by, revoked_at)`,
	`CREATE TABLE IF NOT EXISTS api_webhooks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_id INTEGER NOT NULL DEFAULT 0,
  url TEXT NOT NULL,
  secret_enc TEXT NOT NULL,
  events TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  fail_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_api_webhooks_owner ON api_webhooks(owner_id)`,
	`CREATE TABLE IF NOT EXISTS api_webhook_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  webhook_id INTEGER NOT NULL,
  event_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  attempt INTEGER NOT NULL,
  status_code INTEGER NOT NULL DEFAULT 0,
  ok INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_api_deliveries_hook ON api_webhook_deliveries(webhook_id, id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_deliveries_created ON api_webhook_deliveries(created_at)`,
}

func EnsureSQLiteSchema(db *sql.DB) error {
	for i, st := range sqliteStatements {
		if _, err := db.Exec(st); err != nil {
			logger.Error("apikit: ensure schema statement %d failed: %v", i, err)
			return fmt.Errorf("apikit: ensure schema statement %d: %w", i, err)
		}
	}
	return nil
}
