package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileResolver(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "entsec-identity", "flock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hr-feed-token"), []byte("not-a-real-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := FileResolver{Root: root}
	got, err := r.Resolve(context.Background(), "strongbox://entsec-identity/flock/hr-feed-token")
	if err != nil || string(got) != "not-a-real-token" {
		t.Fatalf("Resolve = %q, %v", got, err)
	}
	if _, err := r.Resolve(context.Background(), "strongbox://entsec-identity/flock/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing reference: err = %v", err)
	}
	for _, bad := range []string{"file:///etc/passwd", "strongbox://../etc/passwd", "strongbox://a//b", "strongbox://x/{tenant}/key", "strongbox://"} {
		if _, err := r.Resolve(context.Background(), bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s: expected a malformed-reference error, got %v", bad, err)
		}
	}
}
