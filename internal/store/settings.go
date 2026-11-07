package store

import (
	"database/sql"
	"fmt"
	"runtime"
	"strings"
)

// tokenPrefixes are the accepted prefixes per Slack token setting. A rotating app token is xoxe.xapp-;
// a bare xoxe. is a configuration token, which Socket Mode rejects (token_expired).
var tokenPrefixes = map[string][]string{
	"slack_app_token": {"xapp-", "xoxe.xapp-"},
	"slack_bot_token": {"xoxb-"},
}

// DefaultTerminalTemplate returns the OS-specific terminal launch command template.
func DefaultTerminalTemplate() string {
	switch runtime.GOOS {
	case "darwin":
		return `osascript -e 'tell application "Terminal" to do script "cd {cwd} && {cmd}"' -e 'tell application "Terminal" to activate'`
	case "windows":
		return `wt -d {cwd} cmd.exe /k {cmd}`
	default: // linux, bsd, etc.
		return `x-terminal-emulator -e sh -c "cd {cwd} && {cmd}; exec ${SHELL:-bash}" &`
	}
}

// Defaults are the setting values used when a key is unset (tech-spec §5).
var Defaults = map[string]string{
	"key_prefix":               "CV",
	"user_name":                "",
	"max_parallel":             "3",
	"resume_threshold":         "60000",
	"comment_debounce_secs":    "20",
	"max_consecutive_failures": "3",
	"default_model":            "",
	"default_runner":           "claude", // "claude" or "agy"
	"terminal_template":        DefaultTerminalTemplate(),
	"brief_extra_rules":        "",
	"prompt_fresh":             "",
	"prompt_resume":            "",
	"pr_comment_prefix":        "ai agent: ",
	"chat_enabled":             "on", // "off" pauses the chat bridge (e.g. while at the desktop)
	"chat_transport":           "slack",
	"chat_statuses":            "needs_you,review,done,failed", // which system status changes send a message
	"slack_bot_token":          "",                             // xoxb-…: posts DMs (see internal/slack)
	"slack_user_token":         "",                             // xoxp-…: optional; when set, all posts are made as you (into your self-DM) instead of the bot
	"slack_app_token":          "",                             // xapp-…: Socket Mode, scope connections:write
	"slack_app_refresh_token":  "",                             // xoxe-1-…: optional; rotates a 12h xoxe.xapp- slack_app_token when it expires
	"slack_client_id":          "",                             // app Client ID: needed with slack_app_refresh_token (oauth.v2.access)
	"slack_client_secret":      "",                             // app Client Secret: needed with slack_app_refresh_token
	"slack_user_id":            "",                             // your Slack member id: the only user the bridge DMs or obeys
}

func getSetting(db dbtx, key string) string {
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return Defaults[key]
	}
	return v
}

// GetSetting returns the stored value, else the default, else "".
func GetSetting(db *sql.DB, key string) string { return getSetting(db, key) }

// SetSetting upserts a setting.
func SetSetting(db *sql.DB, key, value string) error {
	if pre, ok := tokenPrefixes[key]; ok && value != "" {
		okPrefix := false
		for _, p := range pre {
			okPrefix = okPrefix || strings.HasPrefix(value, p)
		}
		if !okPrefix {
			return fmt.Errorf("%s must start with %s", key, strings.Join(pre, " or "))
		}
	}
	_, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// ListSettings returns every setting, defaults overlaid with stored values.
func ListSettings(db *sql.DB) map[string]string {
	out := make(map[string]string, len(Defaults))
	for k, v := range Defaults {
		out[k] = v
	}
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}
