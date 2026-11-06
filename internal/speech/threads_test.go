package speech

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBackgroundThreads(t *testing.T) {
	l := &Local{Threads: 4, BackThreads: 3}
	if got := l.threads(context.Background()); got != 4 {
		t.Fatalf("dictation threads = %d, want 4", got)
	}
	if got := l.threads(Background(context.Background())); got != 3 {
		t.Fatalf("background threads = %d, want 3", got)
	}
	if DefaultThreads() > 4 || DefaultBackThreads() < 1 || DefaultBackThreads() > DefaultThreads() {
		t.Fatalf("defaults %d/%d", DefaultThreads(), DefaultBackThreads())
	}
	if look("nice") != "" {
		if cmd := command(Background(context.Background()), "/bin/true", "x"); filepath.Base(cmd.Path) != "nice" || cmd.Args[2] != "19" {
			t.Fatalf("background command = %v", cmd.Args)
		}
	}
	if cmd := command(context.Background(), "/bin/true", "x"); cmd.Path != "/bin/true" {
		t.Fatalf("dictation command = %v", cmd.Args)
	}
}
