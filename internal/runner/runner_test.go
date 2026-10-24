package runner

import (
	"testing"
)

func TestForTask(t *testing.T) {
	tests := []struct {
		taskRunner    string
		defaultRunner string
		wantName      string
	}{
		{"", "", "claude"},
		{"claude", "", "claude"},
		{"agy", "", "agy"},
		{"", "agy", "agy"},
		{"claude", "agy", "claude"},
		{"agy", "claude", "agy"},
	}
	for _, tc := range tests {
		r := ForTask(tc.taskRunner, tc.defaultRunner, "/tmp")
		if got := r.Name(); got != tc.wantName {
			t.Errorf("ForTask(%q, %q).Name() = %q, want %q", tc.taskRunner, tc.defaultRunner, got, tc.wantName)
		}
	}
}

func TestValidateRunner(t *testing.T) {
	if err := ValidateRunner(""); err != nil {
		t.Errorf("expected empty runner valid, got: %v", err)
	}
	if err := ValidateRunner("claude"); err != nil {
		t.Errorf("expected claude valid, got: %v", err)
	}
	if err := ValidateRunner("agy"); err != nil {
		t.Errorf("expected agy valid, got: %v", err)
	}
	if err := ValidateRunner("invalid"); err == nil {
		t.Errorf("expected error for invalid runner")
	}
}
