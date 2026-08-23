package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDomainPackagesDoNotImportInfrastructure(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"../gateway/domain", "../pipeline/domain"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if entry == nil && strings.Contains(walkErr.Error(), "no such file or directory") {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range file.Imports {
				name, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				if forbiddenDomainImport(name) {
					t.Errorf("%s imports forbidden infrastructure package %q", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inspect %s: %v", root, err)
		}
	}
}

func forbiddenDomainImport(importPath string) bool {
	if importPath == "database/sql" || importPath == "net/http" {
		return true
	}
	for _, segment := range []string{"/adapters", "/persistence", "/transport", "/adminui"} {
		if strings.Contains(importPath, segment) {
			return true
		}
	}
	return false
}

var _ ast.Node
