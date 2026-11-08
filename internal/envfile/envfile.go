package envfile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/yamlout"
)

const SourceDir = "src"

func Handle(kit config.Kit) string {
	if kit.IsLocal() {
		return filepath.Base(kit.Source)
	}
	last := kit.Source[strings.LastIndex(kit.Source, "/")+1:]
	last, _, _ = strings.Cut(last, "@")
	last, _, _ = strings.Cut(last, ":")
	return last
}

type Options struct {
	CPUs   *int
	Memory string
}

func Build(composition *config.Composition, sandbox, envDir string, options Options) yamlout.Map {
	var kitEntries []any
	for _, kit := range composition.AllKits() {
		kitEntries = append(kitEntries, kitEntry(kit, envDir))
	}
	env := yamlout.Map{
		{Key: "schemaVersion", Value: "1"},
		{Key: "name", Value: sandbox},
		{Key: "agent", Value: Handle(composition.Base)},
		{Key: "workspace", Value: yamlout.Map{{Key: "path", Value: "./" + SourceDir}, {Key: "clone", Value: true}}},
		{Key: "kits", Value: kitEntries},
	}
	var sandboxOptions yamlout.Map
	if options.CPUs != nil {
		sandboxOptions = append(sandboxOptions, yamlout.Item{Key: "cpus", Value: *options.CPUs})
	}
	if options.Memory != "" {
		sandboxOptions = append(sandboxOptions, yamlout.Item{Key: "memory", Value: options.Memory})
	}
	if len(sandboxOptions) > 0 {
		env = append(env, yamlout.Item{Key: "sandboxOptions", Value: sandboxOptions})
	}
	if len(composition.Secrets) > 0 {
		env = append(env, yamlout.Item{Key: "secrets", Value: composition.Secrets})
	}
	if len(composition.Bindings) > 0 {
		env = append(env, yamlout.Item{Key: "bindings", Value: composition.Bindings})
	}
	return env
}

func Write(env yamlout.Map, envDir string) (string, error) {
	text, err := yamlout.Dump(env)
	if err != nil {
		return "", err
	}
	path := filepath.Join(envDir, "sbxenv.yaml")
	return path, os.WriteFile(path, []byte(text), 0o644)
}

func kitEntry(kit config.Kit, envDir string) any {
	source := kit.Source
	if kit.IsLocal() {
		from, _ := filepath.Abs(envDir)
		if relative, err := filepath.Rel(from, source); err == nil {
			if strings.HasPrefix(relative, "../") {
				source = relative
			} else {
				source = "./" + relative
			}
		}
	}
	if len(kit.Args) == 0 {
		return source
	}
	return yamlout.Map{{Key: "source", Value: source}, {Key: "args", Value: kit.Args}}
}
