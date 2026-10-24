package store

// Timestamps are RFC3339 UTC strings. Nullable columns are pointers.

// Comment authors.
const (
	AuthorHuman  = "human"
	AuthorAgent  = "agent"
	AuthorSystem = "system"
)

// Repo is a registered git clone.
type Repo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	DefaultBranch string `json:"default_branch"`
	CreatedAt     string `json:"created_at"`
}

// Task is a kanban task. Repos is filled by GetTask, GetTaskByKey and ListTasks.
type Task struct {
	ID                  int64      `json:"id"`
	Key                 string     `json:"key"`
	Title               string     `json:"title"`
	Description         string     `json:"description"`
	Status              Status     `json:"status"`
	Priority            int        `json:"priority"`
	Model               *string    `json:"model"`
	Agent               *string    `json:"agent"`
	// Runner overrides the default_runner setting for this task ("claude" or "agy"; nil = default).
	Runner              *string    `json:"runner"`
	Source              string     `json:"source"`
	ExternalKey         *string    `json:"external_key"`
	ExternalURL         *string    `json:"external_url"`
	ParentID            *int64     `json:"parent_id"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	CreatedAt           string     `json:"created_at"`
	UpdatedAt           string     `json:"updated_at"`
	Repos               []TaskRepo `json:"repos,omitempty"`
}

// TaskRepo links a task to a repo. On input to CreateTask, Branch is the
// slug; the store stores cv/<KEY>-<slug>. RepoName is joined on load and
// ignored on write.
type TaskRepo struct {
	TaskID       int64   `json:"task_id"`
	RepoID       int64   `json:"repo_id"`
	RepoName     string  `json:"repo_name"`
	IsPrimary    bool    `json:"is_primary"`
	Branch       string  `json:"branch"`
	WorktreePath *string `json:"worktree_path"`
	PRURL        *string `json:"pr_url"`
	PRState      *string `json:"pr_state"`
}

// Run is one Claude session launch for a task.
type Run struct {
	ID           int64   `json:"id"`
	TaskID       int64   `json:"task_id"`
	Kind         string  `json:"kind"`
	Mode         string  `json:"mode"`
	BgID         *string `json:"bg_id"`
	SessionID    *string `json:"session_id"`
	Status       string  `json:"status"`
	Prompt       string  `json:"prompt"`
	BlockStatus  *string `json:"block_status"`
	Summary      *string `json:"summary"`
	State        *string `json:"state"`
	Next         *string `json:"next"`
	Criteria     *string `json:"criteria"`
	FinalMessage *string `json:"final_message"`
	TokensIn     int64   `json:"tokens_in"`
	TokensOut    int64   `json:"tokens_out"`
	CacheRead    int64   `json:"cache_read"`
	CacheWrite   int64   `json:"cache_write"`
	CtxPeak      int64   `json:"ctx_peak"`
	APICalls     int64   `json:"api_calls"`
	Error        *string `json:"error"`
	StartedAt    string  `json:"started_at"`
	EndedAt      *string `json:"ended_at"`
}

// Comment is a thread entry.
type Comment struct {
	ID              int64  `json:"id"`
	TaskID          int64  `json:"task_id"`
	Author          string `json:"author"`
	Body            string `json:"body"`
	RunID           *int64 `json:"run_id"`
	ConsumedByRunID *int64 `json:"consumed_by_run_id"`
	CreatedAt       string `json:"created_at"`
}

// Event is an audit-log row.
type Event struct {
	ID        int64  `json:"id"`
	TaskID    *int64 `json:"task_id"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// Learning is a proposed lesson.
type Learning struct {
	ID            int64   `json:"id"`
	TaskID        *int64  `json:"task_id"`
	RunID         *int64  `json:"run_id"`
	RepoID        *int64  `json:"repo_id"`
	Body          string  `json:"body"`
	Status        string  `json:"status"`
	Target        *string `json:"target"`
	AppliedTaskID *int64  `json:"applied_task_id"`
	CreatedAt     string  `json:"created_at"`
}

// Automation is a scheduled action.
type Automation struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Schedule   string  `json:"schedule"`
	Precheck   string  `json:"precheck"`
	ActionKind string  `json:"action_kind"`
	ActionArg  string  `json:"action_arg"`
	Enabled    bool    `json:"enabled"`
	LastRunAt  *string `json:"last_run_at"`
	LastResult *string `json:"last_result"`
}
