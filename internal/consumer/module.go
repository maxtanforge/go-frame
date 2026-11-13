package consumer

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"golang.org/x/mod/modfile"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// prepareService copies the reference module into this run's temporary root.
// The module name stays unchanged; only its framework selection is adjusted.
func prepareService(source, destination, version string) error {
	if err := copyFixture(source, destination); err != nil {
		return err
	}
	path := filepath.Join(destination, "go.mod")
	manifest, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, err := modfile.Parse("go.mod", manifest, nil)
	if err != nil || file.Module == nil || file.Module.Mod.Path != "example.com/service" || len(file.Replace) != 0 {
		return errors.New("invalid service template module")
	}
	found := false
	for _, requirement := range file.Require {
		if requirement.Mod.Path == framework {
			found = true
		}
	}
	if !found {
		return errors.New("service template framework requirement missing")
	}
	if err = file.AddRequire(framework, version); err != nil {
		return err
	}
	manifest, err = file.Format()
	if err != nil {
		return err
	}
	return os.WriteFile(path, manifest, 0600)
}

func fixtureManifest(source []byte, version string, remote bool) ([]byte, error) {
	file, e := modfile.Parse("go.mod", source, nil)
	if e != nil || file.Module == nil || file.Go == nil {
		return nil, errors.New("invalid source module")
	}
	if remote && len(file.Replace) > 0 {
		return nil, errors.New("remote source replacements rejected")
	}
	for _, replacement := range file.Replace {
		if e = file.DropReplace(replacement.Old.Path, replacement.Old.Version); e != nil {
			return nil, e
		}
	}
	if e = file.AddModuleStmt(fixtureModule); e != nil {
		return nil, e
	}
	if e = file.AddRequire(framework, version); e != nil {
		return nil, e
	}
	file.Cleanup()
	return file.Format()
}

func fixtureHasTest(dir, name string) (bool, error) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return false, e
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, e := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if e != nil {
			return false, e
		}
		for _, decl := range file.Decls {
			if function, ok := decl.(*ast.FuncDecl); ok && function.Name.Name == name && function.Recv == nil {
				return true, nil
			}
		}
	}
	return false, nil
}

func pinSourceGraph(manifest, graph []byte) ([]byte, error) {
	file, e := modfile.Parse("go.mod", manifest, nil)
	if e != nil {
		return nil, e
	}
	decoder := json.NewDecoder(bytes.NewReader(graph))
	for {
		var module struct {
			Path, Version string
			Replace       *struct{ Path string }
		}
		e = decoder.Decode(&module)
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, errors.New("invalid source module graph")
		}
		if module.Replace != nil {
			return nil, errors.New("source graph replacement rejected")
		}
		if module.Version != "" && module.Path != framework {
			if e = file.AddRequire(module.Path, module.Version); e != nil {
				return nil, e
			}
		}
	}
	file.Cleanup()
	return file.Format()
}
