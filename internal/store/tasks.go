package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const taskCols = `id, key, title, description, status, priority, model, agent, runner, source, external_key,
 external_url, parent_id, consecutive_failures, created_at, updated_at`

func scanTask(r interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := r.Scan(&t.ID, &t.Key, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Model, &t.Agent, &t.Runner, &t.Source,
		&t.ExternalKey, &t.ExternalURL, &t.ParentID, &t.ConsecutiveFailures, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}
func loadRepos(db dbtx, taskID int64) ([]TaskRepo, error) {
	rows, err := db.Query(`SELECT tr.task_id, tr.repo_id, r.name, tr.is_primary, tr.branch, tr.worktree_path, tr.pr_url, tr.pr_state
 FROM task_repos tr JOIN repos r ON r.id=tr.repo_id WHERE tr.task_id=? ORDER BY tr.is_primary DESC, r.name`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task repos: %w", err)
	}
	defer rows.Close()
	var out []TaskRepo
	for rows.Next() {
		var x TaskRepo
		if err := rows.Scan(&x.TaskID, &x.RepoID, &x.RepoName, &x.IsPrimary, &x.Branch, &x.WorktreePath, &x.PRURL, &x.PRState); err != nil {
			return nil, fmt.Errorf("scan task repo: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func getTask(db dbtx, where string, arg any, what string) (Task, error) {
	t, err := scanTask(db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE `+where, arg))
	if err != nil {
		return Task{}, fmt.Errorf("task %s: %w", what, err)
	}
	if t.Repos, err = loadRepos(db, t.ID); err != nil {
		return Task{}, err
	}
	return t, nil
}

// GetTask returns the task with its Repos, or a wrapped sql.ErrNoRows.
func GetTask(db *sql.DB, id int64) (Task, error) {
	return getTask(db, `id=?`, id, strconv.FormatInt(id, 10))
}

// GetTaskByKey is GetTask by key (e.g. "CV-3").
func GetTaskByKey(db *sql.DB, key string) (Task, error) {
	return getTask(db, `key=?`, key, key)
}

// CreateTask inserts a task and its repo links in one transaction and assigns
// key = <key_prefix>-<N>, or ChildKey of the parent when ParentID is set. Exactly one repo must be primary. Each repo's Branch
// is taken as the slug and stored as cv/<KEY>-<slug>. Status defaults to backlog.
func CreateTask(db *sql.DB, t Task, repos []TaskRepo) (Task, error) {
	primaries := 0
	for _, r := range repos {
		if r.IsPrimary {
			primaries++
		}
	}
	if primaries != 1 {
		return Task{}, fmt.Errorf("task needs exactly one primary repo, got %d", primaries)
	}
	if t.Status == "" {
		t.Status = StatusBacklog
	}
	if t.Source == "" {
		t.Source = "manual"
	}
	t.CreatedAt = now()
	t.UpdatedAt = t.CreatedAt

	var id int64
	err := inTx(db, func(tx *sql.Tx) error {
		prefix := getSetting(tx, "key_prefix")
		var n int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(CAST(substr(key, ?) AS INTEGER)), 0) + 1 FROM tasks WHERE substr(key, ?) NOT GLOB '*[^0-9]*' AND key LIKE ? ESCAPE '\'`,
			len(prefix)+2, len(prefix)+2, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)+`-%`).Scan(&n); err != nil {
			return fmt.Errorf("next key: %w", err)
		}
		t.Key = fmt.Sprintf("%s-%d", prefix, n)
		if t.ParentID != nil {
			var pkey string
			if err := tx.QueryRow(`SELECT key FROM tasks WHERE id=?`, *t.ParentID).Scan(&pkey); err != nil {
				return fmt.Errorf("parent task %d: %w", *t.ParentID, err)
			}
			var err error
			if t.Key, err = nextChildKey(tx, *t.ParentID, pkey); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`INSERT INTO tasks (key, title, description, status, priority, model, agent, runner, source, external_key,
 external_url, parent_id, consecutive_failures, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.Key, t.Title, t.Description, t.Status, t.Priority, t.Model, t.Agent, t.Runner, t.Source, t.ExternalKey,
			t.ExternalURL, t.ParentID, t.ConsecutiveFailures, t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert task: %w", err)
		}
		id, _ = res.LastInsertId()
		for _, r := range repos {
			if _, err := tx.Exec(`INSERT INTO task_repos (task_id, repo_id, is_primary, branch, worktree_path, pr_url, pr_state) VALUES (?,?,?,?,?,?,?)`,
				id, r.RepoID, r.IsPrimary, fmt.Sprintf("cv/%s-%s", t.Key, r.Branch), r.WorktreePath, r.PRURL, r.PRState); err != nil {
				return fmt.Errorf("link repo %d: %w", r.RepoID, err)
			}
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return GetTask(db, id)
}

// ListTasks returns tasks (with Repos), priority desc then created_at asc,
// optionally filtered to the given statuses.
func ListTasks(db *sql.DB, statuses ...Status) ([]Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks`
	var args []any
	if len(statuses) > 0 {
		q += ` WHERE status IN (` + placeholders(len(statuses)) + `)`
		for _, s := range statuses {
			args = append(args, string(s))
		}
	}
	rows, err := db.Query(q+` ORDER BY priority DESC, created_at ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	rows.Close() // single connection: free it before loading repos
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Repos, err = loadRepos(db, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// UpdateTaskStatus moves a task if the transition is legal for actor, using
// optimistic concurrency on the current status, and records a status_changed event.
func UpdateTaskStatus(db *sql.DB, id int64, to Status, actor Actor) (Task, error) {
	err := inTx(db, func(tx *sql.Tx) error {
		var from Status
		if err := tx.QueryRow(`SELECT status FROM tasks WHERE id=?`, id).Scan(&from); err != nil {
			return fmt.Errorf("task %d: %w", id, err)
		}
		if err := ValidateTransition(from, to, actor); err != nil {
			return err
		}
		// A run ending (needs_you/review) with human comments still queued goes back to todo
		// so the next run gets them. Failed is excluded: a launch-failure cap must still stop.
		if from == StatusRunning && actor == ActorSystem && (to == StatusNeedsYou || to == StatusReview) {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM comments WHERE task_id=? AND author='human' AND consumed_by_run_id IS NULL`, id).Scan(&n); err != nil {
				return fmt.Errorf("queued comments: %w", err)
			}
			if n > 0 {
				to = StatusTodo
			}
		}
		// A human leaving failed (requeue) gets a fresh failure budget.
		reset := from == StatusFailed && actor == ActorHuman
		res, err := tx.Exec(`UPDATE tasks SET status=?, consecutive_failures=CASE WHEN ? THEN 0 ELSE consecutive_failures END, updated_at=? WHERE id=? AND status=?`, to, reset, now(), id, from)
		if err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("task %d changed concurrently (was %s)", id, from)
		}
		return recordEvent(tx, id, "status_changed", fmt.Sprintf("%s -> %s (%s)", from, to, actor))
	})
	if err != nil {
		return Task{}, err
	}
	return GetTask(db, id)
}

// UpdateTask writes title, description, priority, model, agent, runner and consecutive_failures.
func UpdateTask(db *sql.DB, t Task) error {
	res, err := db.Exec(`UPDATE tasks SET title=?, description=?, priority=?, model=?, agent=?, runner=?, consecutive_failures=?, updated_at=? WHERE id=?`,
		t.Title, t.Description, t.Priority, t.Model, t.Agent, t.Runner, t.ConsecutiveFailures, now(), t.ID)
	if err != nil {
		return fmt.Errorf("update task %d: %w", t.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("task %d: %w", t.ID, sql.ErrNoRows)
	}
	return nil
}

// UpdateTaskRepo writes branch, worktree_path, pr_url and pr_state of one link.
func UpdateTaskRepo(db *sql.DB, r TaskRepo) error {
	res, err := db.Exec(`UPDATE task_repos SET branch=?, worktree_path=?, pr_url=?, pr_state=? WHERE task_id=? AND repo_id=?`,
		r.Branch, r.WorktreePath, r.PRURL, r.PRState, r.TaskID, r.RepoID)
	if err != nil {
		return fmt.Errorf("update task repo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("task repo %d/%d: %w", r.TaskID, r.RepoID, sql.ErrNoRows)
	}
	return nil
}

// SetTaskRunner sets (or with "" clears) the task's runner override.
func SetTaskRunner(db *sql.DB, id int64, runner *string) error {
	res, err := db.Exec(`UPDATE tasks SET runner=?, updated_at=? WHERE id=?`, runner, now(), id)
	if err != nil {
		return fmt.Errorf("set task runner: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("task %d: %w", id, sql.ErrNoRows)
	}
	return nil
}
