// Package migrations embeds Denyra's checksummed service schemas.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

//go:embed gateway/*.sql pipeline/*.sql
var files embed.FS

func For(service string) ([]denysqlite.Migration, error) {
	if service != "gateway" && service != "pipeline" {
		return nil, fmt.Errorf("unknown migration service %q", service)
	}
	entries, err := fs.ReadDir(files, service)
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	result := make([]denysqlite.Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		identity := strings.TrimSuffix(entry.Name(), ".sql")
		sequenceText, name, found := strings.Cut(identity, "_")
		if !found {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		sequence, err := strconv.Atoi(sequenceText)
		if err != nil {
			return nil, fmt.Errorf("parse migration sequence %q: %w", entry.Name(), err)
		}
		content, err := files.ReadFile(service + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		result = append(result, denysqlite.Migration{Sequence: sequence, Name: name, SQL: string(content)})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Sequence < result[right].Sequence })
	return result, nil
}
