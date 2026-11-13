package consumer

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// checkBusiness validates a business-owned module copied outside the repository.
func (r *runner) checkBusiness(source, destination string, o Options) error {
	if _, err := os.Stat(filepath.Join(source, "examples", "business")); os.IsNotExist(err) && o.Mode == "remote" {
		r.skip("business_fixture", "selected_version_example_unavailable")
		return nil
	}
	if err := r.step("business_fixture", func() error {
		if err := copyBusinessFixture(filepath.Join(source, "examples", "business"), destination); err != nil {
			return err
		}
		original, err := os.ReadFile(filepath.Join(source, "go.mod"))
		if err != nil {
			return err
		}
		manifest, err := fixtureManifest(original, r.report.ResolvedVersion, o.Mode == "remote")
		if err != nil {
			return err
		}
		graph, err := run(r.ctx, source, r.env, "go", "list", "-mod=readonly", "-m", "-json", "all")
		if err != nil {
			return err
		}
		manifest, err = pinSourceGraph(manifest, graph)
		if err != nil {
			return err
		}
		file, err := modfile.Parse("go.mod", manifest, nil)
		if err != nil {
			return err
		}
		if err := file.AddModuleStmt("example.org/consumer/business"); err != nil {
			return err
		}
		manifest, err = file.Format()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, "go.mod"), manifest, 0600); err != nil {
			return err
		}
		sums, err := os.ReadFile(filepath.Join(source, "go.sum"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, "go.sum"), sums, 0600); err != nil {
			return err
		}
		if o.Mode == "workspace" {
			_, err = run(r.ctx, destination, r.env, "go", "mod", "edit", "-replace="+framework+"="+o.Repo)
		}
		return err
	}); err != nil {
		return errors.New("business fixture failed")
	}
	for _, item := range []struct {
		name string
		args []string
	}{
		{"business_tidy", []string{"mod", "tidy"}},
		{"business_test", []string{"test", "./...", "-count=1"}},
		{"business_vet", []string{"vet", "./..."}},
		{"business_build", []string{"build", "-buildvcs=false", "./..."}},
	} {
		if err := r.step(item.name, func() error { _, err := run(r.ctx, destination, r.env, "go", item.args...); return err }); err != nil {
			return errors.New("business checks failed")
		}
	}
	if o.Race {
		if err := r.step("business_race", func() error {
			_, err := run(r.ctx, destination, r.env, "go", "test", "-race", "./...", "-count=1")
			return err
		}); err != nil {
			return errors.New("business race failed")
		}
	}
	return nil
}
