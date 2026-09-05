// Package migrations holds the embedded, forward-only SQL schema migrations.
//
// The .sql files live here rather than inside internal/store because
// 03-DATA-MODEL.md §1.1 fixes their location, and go:embed cannot reach a
// parent directory — so the embed directive has to sit next to them. The
// directory belongs to WP-B2 (08-ROADMAP.md §4); internal/store/migrate.go is
// the only consumer.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Migration is one numbered schema step. Version is the integer prefix of the
// filename, so 0001_init.sql is version 1.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// All returns every embedded migration in ascending version order. It fails if
// a filename does not match <digits>_<name>.sql or if two files share a
// version — both are packaging mistakes that must not reach a database.
func All() ([]Migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	out := make([]Migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseVersion(e.Name())
		if err != nil {
			return nil, err
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations: %q and %q share version %d", other, e.Name(), version)
		}
		seen[version] = e.Name()

		body, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: version, Name: e.Name(), SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func parseVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok || prefix == "" {
		return 0, fmt.Errorf("migrations: %q must be named <version>_<name>.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migrations: %q has no positive integer version prefix", name)
	}
	return version, nil
}
