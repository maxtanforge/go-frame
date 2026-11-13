// Package loader composes explicit YAML, development dotenv, captured environment
// and CLI inputs. It is optional; config and command do not depend on it.
package loader

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maxtanforge/go-frame/config"
	"github.com/maxtanforge/go-frame/config/dotenv"
	configyaml "github.com/maxtanforge/go-frame/config/yaml"
)

// Options controls input capture and explicitly permitted application policies.
// Lookup is required and is called only for declared environment names, at New.
// AllowDotenv must identify development configurations from the final value.
// Without it --env-file is rejected. AllowWatch only permits parsing the flag;
// the consumer is responsible for implementing watching and applying updates.
type Options[T any] struct {
	Lookup      func(string) (string, bool)
	AllowDotenv func(T) bool
	AllowWatch  bool
}

// Inputs captures defaults, environment and overrides once. Load rereads only
// explicitly named files. Like config.Decode, excluded fields and custom text
// types' internals remain caller-owned and must be immutable.
type Inputs[T any] struct {
	file, envFile string
	watch         bool
	prepared      *config.Prepared[T]
	environment   map[string]string
	set           overrides
	allowDotenv   func(T) bool
}

type overrides map[string]string

func (o overrides) String() string { return "explicit configuration overrides" }
func (o overrides) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return errors.New("expected path=value")
	}
	if _, exists := o[k]; exists {
		return errors.New("duplicate override path")
	}
	o[k] = v
	return nil
}

// New parses --config, --env-file, repeatable --set path=value and, when enabled,
// --watch-config. It checks the schema and captures defaults and environment;
// business validation runs only in Load after all sources have been applied.
func New[T any](defaults T, args []string, opts Options[T]) (*Inputs[T], error) {
	if opts.Lookup == nil {
		return nil, errors.New("loader: environment lookup is required")
	}
	in := &Inputs[T]{set: overrides{}, environment: map[string]string{}, allowDotenv: opts.AllowDotenv}
	f := flag.NewFlagSet("loader", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&in.file, "config", "", "explicit YAML path")
	f.StringVar(&in.envFile, "env-file", "", "explicit development dotenv path")
	if opts.AllowWatch {
		f.BoolVar(&in.watch, "watch-config", false, "watch explicit configuration files")
	}
	f.Var(in.set, "set", "explicit path=value (repeatable)")
	if err := f.Parse(args); err != nil {
		// flag 的错误文本会回显未识别参数名，违反“错误不含输入”的库约定；
		// 仅对不含输入的 flag.ErrHelp 保留 cause，调用方可据此特判 help。
		if errors.Is(err, flag.ErrHelp) {
			return nil, fmt.Errorf("loader: parse configuration flags: %w", err)
		}
		return nil, errors.New("loader: invalid configuration flags")
	}
	if f.NArg() != 0 {
		return nil, errors.New("loader: unexpected positional arguments")
	}
	if in.envFile != "" && opts.AllowDotenv == nil {
		return nil, errors.New("loader: development dotenv policy is required")
	}
	if in.watch && in.file == "" && in.envFile == "" {
		return nil, errors.New("loader: --watch-config requires an explicit configuration file")
	}
	for _, path := range []*string{&in.file, &in.envFile} {
		if *path == "" {
			continue
		}
		abs, err := filepath.Abs(*path)
		if err != nil {
			return nil, errors.New("loader: invalid configuration path")
		}
		*path = abs
	}
	schema, err := config.Prepare(defaults)
	if err != nil {
		return nil, err
	}
	in.prepared = schema
	for _, entry := range schema.Entries() {
		if entry.Env != "" {
			if value, ok := opts.Lookup(entry.Env); ok {
				in.environment[entry.Env] = value
			}
		}
	}
	return in, nil
}

// Watch reports the explicitly requested watch mode. It starts no goroutines.
func (in *Inputs[T]) Watch() bool { return in.watch }

// Paths returns a fresh list of absolute, explicitly named file paths.
func (in *Inputs[T]) Paths() []string {
	var paths []string
	for _, path := range []string{in.file, in.envFile} {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// Load applies defaults < YAML < dotenv < captured environment < CLI overrides.
// It never changes the process environment or discovers configuration files.
func (in *Inputs[T]) Load() (config.Result[T], error) {
	var zero config.Result[T]
	sources := make([]config.Source, 0, 4)
	for _, input := range []struct {
		path  string
		parse func(io.Reader) (config.Source, error)
	}{{in.file, configyaml.Parse}, {in.envFile, dotenv.Parse}} {
		if input.path == "" {
			continue
		}
		r, err := os.Open(input.path)
		if err != nil {
			return zero, errors.New("loader: cannot open explicit configuration file")
		}
		source, parseErr := input.parse(r)
		closeErr := r.Close()
		if parseErr != nil {
			return zero, parseErr
		}
		if closeErr != nil {
			return zero, errors.New("loader: cannot close configuration file")
		}
		sources = append(sources, source)
	}
	sources = append(sources, config.Source{Name: "environment", Lookup: func(key string) (string, bool) { v, ok := in.environment[key]; return v, ok }}, config.Source{Name: "cli", Values: map[string]string(in.set)})
	r, err := in.prepared.Decode(sources...)
	if err != nil {
		return zero, err
	}
	if in.envFile != "" && !in.allowDotenv(r.Value) {
		return zero, errors.New("loader: --env-file is only allowed for development configuration")
	}
	return r, nil
}
