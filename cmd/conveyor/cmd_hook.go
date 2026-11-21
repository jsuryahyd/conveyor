package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/hook"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
	"github.com/JayaSuryaOolio/conveyor/internal/workspace"
)

func init() { register("hook", runHook, "handle a Claude Code hook event (internal)") }

// runHook never returns an error: Claude Code must not be blocked. Failures go to logs/hook.log.
// Every event but PreToolUse (one per tool call, too noisy) leaves a line, so "no Stop for that session" is checkable.
func runHook(home string, args []string) (err error) {
	if pluginCopy() {
		return nil
	}
	var raw []byte
	defer func() {
		if r := recover(); r != nil { // a panic would otherwise vanish into Claude Code's hook output
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
		if err != nil || len(args) != 1 || args[0] != "PreToolUse" {
			logHook(home, args, raw, err)
		}
		err = nil
	}()
	if len(args) != 1 {
		return fmt.Errorf("usage: conveyor hook <Event>")
	}
	raw, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return doHook(home, args, raw)
}

// pluginCopy reports a plugin hook in a session whose --settings (claude.WriteSettings) runs the same
// hook: Claude Code runs both, so the plugin's copy stands down.
func pluginCopy() bool {
	return os.Getenv("CLAUDE_PLUGIN_ROOT") != "" && os.Getenv("CONVEYOR_HOOKS") != ""
}

func doHook(home string, args []string, raw []byte) error {
	if args[0] == "PreToolUse" && hook.IsAsk(raw) { // allowed; its questions go to the task thread
		db, err := store.Open(core.DBPath(home))
		if err != nil {
			return err
		}
		defer db.Close()
		return hook.OnAsk(db, home, raw)
	}
	if args[0] == "PreToolUse" { // answers on stdout, as does a Stop the completion check blocks
		var openErr error
		withDB := func(f func(db *sql.DB)) { // opened per use: only gh commands reach here
			db, err := store.Open(core.DBPath(home))
			if err != nil {
				openErr = err
				return
			}
			defer db.Close()
			f(db)
		}
		res := hook.PreToolUse(raw, func() (pre string) {
			withDB(func(db *sql.DB) { pre = store.GetSetting(db, "pr_comment_prefix") })
			return
		}, func(cwd string) (member bool) {
			withDB(func(db *sql.DB) { member = hook.EpicMember(db, home, cwd) })
			return
		})
		fmt.Fprintf(out, "%s", res)
		hook.AuditCommand(home, raw, len(res) > 0)
		return openErr
	}
	db, err := store.Open(core.DBPath(home))
	if err != nil {
		return err
	}
	defer db.Close()
	res, err := hook.Handle(db, home, args[0], raw)
	fmt.Fprint(out, res) // a blocked Stop's {"decision":"block"} answer
	return err
}

func logHook(home string, args []string, raw []byte, err error) {
	var p struct {
		SessionID           string `json:"session_id"`
		SessionIDCamel      string `json:"sessionId"`
		ConversationID      string `json:"conversation_id"`
		ConversationIDCamel string `json:"conversationId"`
		Cwd                 string `json:"cwd"`
	}
	json.Unmarshal(raw, &p) // best effort: a bad payload still gets its line, with the error
	sess := p.SessionID
	if sess == "" {
		sess = p.SessionIDCamel
	}
	if sess == "" {
		sess = p.ConversationID
	}
	if sess == "" {
		sess = p.ConversationIDCamel
	}
	key := workspace.KeyFromCwd(home, p.Cwd)
	if err == nil && key == "" {
		return // user-level hooks fire for every Claude session; only ours are worth a line
	}
	f, e := core.OpenLog(core.HookLogPath(home))
	if e != nil {
		return
	}
	defer f.Close()
	res := "ok"
	if err != nil {
		res = err.Error()
	}
	fmt.Fprintf(f, "%s %s %v session=%s cwd=%s: %s\n", time.Now().UTC().Format(time.RFC3339), key, args, sess, p.Cwd, res)
}
