package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectCommand(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	// Run detect without apply
	code := run([]string{"--home", dir, "detect"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("detect exited %d: %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Detected agent runtimes:") || !strings.Contains(output, "Default runner:") {
		t.Fatalf("unexpected detect output:\n%s", output)
	}

	// Run detect with --json
	stdout.Reset()
	code = run([]string{"--home", dir, "detect", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("detect --json exited %d: %s", code, stderr.String())
	}
	jsonOutput := stdout.String()
	if !strings.Contains(jsonOutput, `"current_runner"`) || !strings.Contains(jsonOutput, `"agents"`) {
		t.Fatalf("unexpected json output:\n%s", jsonOutput)
	}

	// Run detect with --apply
	stdout.Reset()
	code = run([]string{"--home", dir, "detect", "--apply"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("detect --apply exited %d: %s", code, stderr.String())
	}
	_ = filepath.Join(dir, "conveyor.db")
}
