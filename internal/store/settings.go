package store

import (
	"database/sql"
	"fmt"
)

// Defaults are the setting values used when a key is unset (tech-spec §5).
var Defaults = map[string]string{
	"key_prefix":               "CV",
	"max_parallel":             "3",
	"resume_threshold":         "60000",
	"comment_debounce_secs":    "20",
	"max_consecutive_failures": "3",
	"default_model":            "",
	"default_runner":           "claude", // "claude" or "agy"
	"terminal_template":        `osascript -e 'tell application "Terminal" to do script "cd {cwd} && {cmd}"' -e 'tell application "Terminal" to activate'`,
	"brief_extra_rules":        "",
	"prompt_fresh":             "",
	"prompt_resume":            "",
	"pr_comment_prefix":        "ai agent: ",
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
