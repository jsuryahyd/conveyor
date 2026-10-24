package store

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS repos (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  path TEXT NOT NULL UNIQUE,
  default_branch TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id INTEGER PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'backlog',
  priority INTEGER NOT NULL DEFAULT 0,
  model TEXT,
  agent TEXT,
  runner TEXT,
  source TEXT NOT NULL DEFAULT 'manual',
  external_key TEXT UNIQUE,
  external_url TEXT,
  parent_id INTEGER REFERENCES tasks(id),
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS task_repos (
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  repo_id INTEGER NOT NULL REFERENCES repos(id),
  is_primary INTEGER NOT NULL DEFAULT 0,
  branch TEXT NOT NULL,
  worktree_path TEXT,
  pr_url TEXT,
  pr_state TEXT,
  PRIMARY KEY (task_id, repo_id)
);

CREATE TABLE IF NOT EXISTS runs (
  id INTEGER PRIMARY KEY,
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  mode TEXT NOT NULL,
  bg_id TEXT,
  session_id TEXT,
  status TEXT NOT NULL,
  prompt TEXT NOT NULL DEFAULT '',
  block_status TEXT,
  summary TEXT, state TEXT, next TEXT, criteria TEXT,
  final_message TEXT,
  tokens_in INTEGER NOT NULL DEFAULT 0, tokens_out INTEGER NOT NULL DEFAULT 0,
  cache_read INTEGER NOT NULL DEFAULT 0, cache_write INTEGER NOT NULL DEFAULT 0,
  ctx_peak INTEGER NOT NULL DEFAULT 0,
  api_calls INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  started_at TEXT NOT NULL,
  ended_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_runs_task ON runs(task_id);
CREATE INDEX IF NOT EXISTS idx_runs_session ON runs(session_id);

CREATE TABLE IF NOT EXISTS comments (
  id INTEGER PRIMARY KEY,
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  author TEXT NOT NULL,
  body TEXT NOT NULL,
  run_id INTEGER REFERENCES runs(id),
  consumed_by_run_id INTEGER REFERENCES runs(id),
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_comments_task ON comments(task_id);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  task_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS learnings (
  id INTEGER PRIMARY KEY,
  task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
  run_id INTEGER REFERENCES runs(id) ON DELETE SET NULL,
  repo_id INTEGER REFERENCES repos(id),
  body TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'proposed',
  target TEXT,
  applied_task_id INTEGER REFERENCES tasks(id),
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS automations (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  schedule TEXT NOT NULL,
  precheck TEXT NOT NULL DEFAULT '',
  action_kind TEXT NOT NULL,
  action_arg TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run_at TEXT,
  last_result TEXT
);

CREATE TABLE IF NOT EXISTS task_links (
  from_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  to_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  PRIMARY KEY (from_id, to_id, kind)
);

CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS apps (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  command TEXT NOT NULL,
  global INTEGER NOT NULL DEFAULT 0,
  output INTEGER NOT NULL DEFAULT 0
);
`

// Migrate drops the v1 tables and creates the v2 schema if missing. Later
// column additions go here as idempotent migrateAddX steps.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS kanban_task_events; DROP TABLE IF EXISTS kanban_tasks;`); err != nil {
		return fmt.Errorf("drop v1 tables: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM task_links WHERE kind='duplicates'`); err != nil {
		return fmt.Errorf("drop duplicates links: %w", err)
	}
	if err := migrateAddAppGlobal(db); err != nil {
		return err
	}
	return migrateAddTaskRunner(db)
}

// migrateAddTaskRunner adds tasks.runner to databases created before it existed.
func migrateAddTaskRunner(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('tasks') WHERE name='runner'`).Scan(&n); err != nil {
		return fmt.Errorf("check tasks.runner: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN runner TEXT`); err != nil {
			return fmt.Errorf("add tasks.runner: %w", err)
		}
	}
	return nil
}

// migrateAddAppGlobal adds apps.global and apps.output to databases created before they existed.
func migrateAddAppGlobal(db *sql.DB) error {
	for _, col := range []string{"global", "output"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('apps') WHERE name=?`, col).Scan(&n); err != nil {
			return fmt.Errorf("check apps.%s: %w", col, err)
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE apps ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("add apps.%s: %w", col, err)
			}
		}
	}
	return nil
}
