// Package secrets resolves Strongbox references of the form strongbox://<mount>/<path>.
//
// Flock never reads credentials from config files, flags or environment variables. The Strongbox
// agent running next to flockd renders the secrets it is allowed to read under a tmpfs directory
// (STRONGBOX_ROOT, default /var/run/strongbox); FileResolver reads them from there.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const scheme = "strongbox://"

// Resolver returns the value behind a Strongbox reference.
type Resolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// FileResolver reads references from the directory the Strongbox agent renders into.
type FileResolver struct {
	Root string
}

// ErrNotFound is returned when the agent has not rendered the reference (missing policy grant).
var ErrNotFound = errors.New("strongbox: reference not rendered")

// Resolve implements Resolver.
func (r FileResolver) Resolve(ctx context.Context, ref string) ([]byte, error) {
	rel, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(string(data), "\r\n")), nil
}

func parseRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, scheme) {
		return "", fmt.Errorf("strongbox: %q is not a strongbox:// reference", ref)
	}
	rel := strings.TrimPrefix(ref, scheme)
	clean := path.Clean("/" + rel)[1:]
	if rel == "" || clean != rel || strings.Contains(rel, "{") {
		return "", fmt.Errorf("strongbox: malformed reference %q", ref)
	}
	return clean, nil
}
