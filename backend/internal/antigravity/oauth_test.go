package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOAuthVolumeRejectsUnexpectedOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"owned", `{"name":"fixture","purpose":"antigravity-oauth"}`, true},
		{"unlabelled", `{"name":"fixture","purpose":""}`, false},
		{"wrong volume", `{"name":"other","purpose":"antigravity-oauth"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s' '"+tc.reply+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			err := checkOAuthVolume(context.Background(), "fixture")
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, ErrContainerCreate) {
				t.Fatalf("ownership check: %v", err)
			}
		})
	}
	for _, volume := range []string{"", "x,src=other", "/tmp/other", "x y"} {
		if err := checkOAuthVolume(context.Background(), volume); !errors.Is(err, ErrContainerCreate) {
			t.Fatal("invalid volume accepted")
		}
	}
}
