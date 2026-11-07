package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPlain(t *testing.T) {
	cases := []struct{ cwd, cmd, want string }{
		{"/a b", "ls", `cd '/a b' && ls`},
		{"/it's", "ls", `cd '/it'\''s' && ls`},
		{`/q"d`, "ls", `cd '/q"d' && ls`},
		{"/$HOME", "echo $X", `cd '/$HOME' && echo $X`},
		{"/x", "", "cd '/x' && :"},
	}
	for _, c := range cases {
		if got := Render("cd {cwd} && {cmd}", c.cwd, c.cmd); got != c.want {
			t.Errorf("got %s want %s", got, c.want)
		}
	}
}

// TestOpenOsascript runs the default template against a fake osascript and checks the
// AppleScript source decodes back to the intended shell line.
func TestOpenOsascript(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + log + "; done\n"
	if err := os.WriteFile(filepath.Join(bin, "osascript"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	cwd, cmd := `/my dir/it's "q" $x`, `claude 'a b' "c"`
	macTemplate := `osascript -e 'tell application "Terminal" to do script "cd {cwd} && {cmd}"' -e 'tell application "Terminal" to activate'`
	if err := Open(macTemplate, cwd, cmd); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 || lines[0] != "-e" || lines[2] != "-e" || !strings.Contains(lines[3], "activate") {
		t.Fatalf("argv %q", lines)
	}
	const pre, post = `tell application "Terminal" to do script "`, `"`
	if !strings.HasPrefix(lines[1], pre) || !strings.HasSuffix(lines[1], post) {
		t.Fatalf("script %q", lines[1])
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(lines[1], pre), post)
	got := strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(inner) // AppleScript unescape
	if want := "cd " + shq(cwd) + " && " + cmd; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
