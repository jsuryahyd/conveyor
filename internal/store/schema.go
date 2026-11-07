package store

import (
	"database/sql"
	"fmt"
	"runtime"
)

const schema = `
CREATE TABLE IF NOT EXISTS repos (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  path TEXT NOT NULL UNIQUE,
  default_branch TEXT NOT NULL,
  created_at TEXT NOT NULL,
  setup_script TEXT NOT NULL DEFAULT ''
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
  hook_denials INTEGER NOT NULL DEFAULT 0, rereads INTEGER NOT NULL DEFAULT 0,
  dup_bash INTEGER NOT NULL DEFAULT 0, big_results INTEGER NOT NULL DEFAULT 0,
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

-- Per-PR review state: head for push detection, watermark for review checks.
-- ponytail: migrate into epic_flow_prs (epic-builder §7) once sub-task B lands.
CREATE TABLE IF NOT EXISTS pr_reviews (
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  repo_id INTEGER NOT NULL REFERENCES repos(id),
  head_sha TEXT NOT NULL DEFAULT '',
  seen_comment_id INTEGER NOT NULL DEFAULT 0,
  unresolved INTEGER NOT NULL DEFAULT 0,
  review TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (task_id, repo_id)
);

CREATE TABLE IF NOT EXISTS task_links (
  from_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  to_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  PRIMARY KEY (from_id, to_id, kind)
);

CREATE TABLE IF NOT EXISTS labels (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE
);

CREATE TABLE IF NOT EXISTS task_labels (
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  label_id INTEGER NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, label_id)
);
CREATE INDEX IF NOT EXISTS idx_task_labels_label ON task_labels(label_id);

CREATE TABLE IF NOT EXISTS epic_flows (
  id INTEGER PRIMARY KEY,
  epic_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  attempt INTEGER NOT NULL,
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL, ended_at TEXT,
  UNIQUE (epic_id, attempt)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_epic_flows_active ON epic_flows(epic_id) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS epic_flow_prs (
  flow_id INTEGER NOT NULL REFERENCES epic_flows(id) ON DELETE CASCADE,
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  repo_id INTEGER REFERENCES repos(id),
  pr_url TEXT, head_sha TEXT, review TEXT, checks TEXT,
  unresolved INTEGER NOT NULL DEFAULT 0,
  seen_comment_id INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE (flow_id, task_id, repo_id)
);

CREATE TABLE IF NOT EXISTS jira_sources (
  epic_id INTEGER PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
  jira_key TEXT NOT NULL,
  default_repo TEXT NOT NULL DEFAULT '',
  interval_sec INTEGER NOT NULL DEFAULT 300,
  status_map TEXT NOT NULL DEFAULT '',
  last_sync TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  done INTEGER NOT NULL DEFAULT 0
);

-- what Conveyor last wrote to Jira for a task: transition name and the branch/PR comment (id + body)
CREATE TABLE IF NOT EXISTS jira_sync (
  task_id INTEGER PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT '',
  comment_id TEXT NOT NULL DEFAULT '',
  comment TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS apps (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  command TEXT NOT NULL,
  output INTEGER NOT NULL DEFAULT 0,
  manifest TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0,
  settings TEXT NOT NULL DEFAULT '{}'
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
	if err := migrateAppColumns(db); err != nil {
		return err
	}
	if err := migrateAddAppManifest(db); err != nil {
		return err
	}
	if err := migrateAddAppDisabled(db); err != nil {
		return err
	}
	if err := migrateAddAppSettings(db); err != nil {
		return err
	}
	if err := migrateAddTaskCreatedBy(db); err != nil {
		return err
	}
	if err := migrateAddTaskRunner(db); err != nil {
		return err
	}
	if err := migrateAddRepoSetupScript(db); err != nil {
		return err
	}
	if err := migrateAddCommentSource(db); err != nil {
		return err
	}
	if err := migrateAddRunWaste(db); err != nil {
		return err
	}
	if err := migrateTerminalTemplate(db); err != nil {
		return err
	}
	return migrateAddAutomationOneShot(db)
}

// migrateTerminalTemplate replaces hardcoded macOS osascript templates with the OS default on non-macOS systems.
func migrateTerminalTemplate(db *sql.DB) error {
	if runtime.GOOS == "darwin" {
		return nil
	}
	macTemplate := `osascript -e 'tell application "Terminal" to do script "cd {cwd} && {cmd}"' -e 'tell application "Terminal" to activate'`
	_, err := db.Exec(`UPDATE settings SET value=? WHERE key='terminal_template' AND value=?`, DefaultTerminalTemplate(), macTemplate)
	return err
}

// migrateAddRunWaste adds the runs waste counts; older runs read 0.
func migrateAddRunWaste(db *sql.DB) error {
	for _, col := range []string{"hook_denials", "rereads", "dup_bash", "big_results"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('runs') WHERE name=?`, col).Scan(&n); err != nil {
			return fmt.Errorf("check runs.%s: %w", col, err)
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("add runs.%s: %w", col, err)
			}
		}
	}
	return nil
}

// migrateAddAutomationOneShot adds automations.run_at/task_id/flow_id.
// flow_id has no FK yet: epic_flows (epic-builder §7) lands in a parallel sub-task.
func migrateAddAutomationOneShot(db *sql.DB) error {
	for col, def := range map[string]string{
		"run_at":  "TEXT",
		"task_id": "INTEGER REFERENCES tasks(id) ON DELETE CASCADE",
		"flow_id": "INTEGER",
	} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('automations') WHERE name=?`, col).Scan(&n); err != nil {
			return fmt.Errorf("check automations.%s: %w", col, err)
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE automations ADD COLUMN ` + col + ` ` + def); err != nil {
				return fmt.Errorf("add automations.%s: %w", col, err)
			}
		}
	}
	return nil
}

// migrateAddRepoSetupScript adds repos.setup_script; existing repos get no script.
func migrateAddRepoSetupScript(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('repos') WHERE name='setup_script'`).Scan(&n); err != nil {
		return fmt.Errorf("check repos.setup_script: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE repos ADD COLUMN setup_script TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add repos.setup_script: %w", err)
		}
	}
	return nil
}

// migrateAddCommentSource adds comments.source (where a comment came from, e.g. "slack"; "" = the app).
func migrateAddCommentSource(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('comments') WHERE name='source'`).Scan(&n); err != nil {
		return fmt.Errorf("check comments.source: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE comments ADD COLUMN source TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add comments.source: %w", err)
		}
	}
	return nil
}

// migrateAddTaskCreatedBy adds tasks.created_by; existing rows stay NULL.
func migrateAddTaskCreatedBy(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('tasks') WHERE name='created_by'`).Scan(&n); err != nil {
		return fmt.Errorf("check tasks.created_by: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN created_by TEXT`); err != nil {
			return fmt.Errorf("add tasks.created_by: %w", err)
		}
	}
	return nil
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

// migrateAppColumns adds apps.output and drops the legacy apps.global on older databases.
func migrateAppColumns(db *sql.DB) error {
	has := func(col string) (n int, err error) {
		err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('apps') WHERE name=?`, col).Scan(&n)
		return n, err
	}
	if n, err := has("output"); err != nil {
		return fmt.Errorf("check apps.output: %w", err)
	} else if n == 0 {
		if _, err := db.Exec(`ALTER TABLE apps ADD COLUMN output INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add apps.output: %w", err)
		}
	}
	if n, err := has("global"); err != nil {
		return fmt.Errorf("check apps.global: %w", err)
	} else if n > 0 {
		if _, err := db.Exec(`ALTER TABLE apps DROP COLUMN global`); err != nil {
			return fmt.Errorf("drop apps.global: %w", err)
		}
	}
	return nil
}

// migrateAddAppManifest adds apps.manifest (the plugin manifest JSON, "" for a plain app).
func migrateAddAppManifest(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('apps') WHERE name='manifest'`).Scan(&n); err != nil {
		return fmt.Errorf("check apps.manifest: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE apps ADD COLUMN manifest TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add apps.manifest: %w", err)
		}
	}
	return nil
}

// migrateAddAppDisabled adds apps.disabled.
func migrateAddAppDisabled(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('apps') WHERE name='disabled'`).Scan(&n); err != nil {
		return fmt.Errorf("check apps.disabled: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE apps ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add apps.disabled: %w", err)
		}
	}
	return nil
}

// migrateAddAppSettings adds apps.settings (JSON object of the user's values for the manifest's settings).
func migrateAddAppSettings(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('apps') WHERE name='settings'`).Scan(&n); err != nil {
		return fmt.Errorf("check apps.settings: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE apps ADD COLUMN settings TEXT NOT NULL DEFAULT '{}'`); err != nil {
			return fmt.Errorf("add apps.settings: %w", err)
		}
	}
	return nil
}
