package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestEveryExampleBookLoadsAndComposesWithoutBindingsSecretsOrCredentials(t *testing.T) {
	root := Resolve("../../examples/books")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			path := filepath.Join(root, entry.Name())
			home := t.TempDir()
			testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [%q]\n", path))
			settings := load(t, home)
			baseline := composeWith(t, settings, Selection{})
			composition := composeWith(t, settings, Selection{Books: []string{path}})
			if !reflect.DeepEqual(composition.Books, []string{entry.Name()}) || len(settings.Books) != 1 {
				t.Fatalf("books = %v; installed = %v", composition.Books, settings.Books)
			}
			if !reflect.DeepEqual(composition.Secrets, baseline.Secrets) || !reflect.DeepEqual(composition.Bindings, baseline.Bindings) {
				t.Fatal("example book adds secrets or bindings")
			}
			book := settings.Books[0]
			if book.Description == "" || len(book.kits) == 0 {
				t.Fatal("example book needs a description and kits")
			}
			for _, entry := range book.kits {
				kit, err := parseKit(entry, path, "example book")
				if err != nil {
					t.Fatal(err)
				}
				if !kit.IsLocal() {
					t.Fatalf("kit is not local: %v", kit)
				}
				documents, err := loadKit(kit.Source)
				if err != nil {
					t.Fatal(err)
				}
				for _, document := range documents {
					for _, capability := range document.spec.Capabilities {
						if !slices.Contains([]string{"com.docker.sandbox/network-policy@2", "com.docker.sandbox/lifecycle@1"}, capability.Type) {
							t.Errorf("unexpected capability: %s", capability.Type)
						}
					}
				}
			}
		})
	}
}

func pythonExampleBook(t *testing.T) *Book {
	t.Helper()
	settings := load(t, t.TempDir())
	book, err := LoadBook(settings.Agents, Resolve("../../examples/books/python"))
	if err != nil {
		t.Fatal(err)
	}
	return book
}

type pythonExampleSpec struct {
	SchemaVersion string `yaml:"schemaVersion"`
	Kind          string `yaml:"kind"`
	Description   string `yaml:"description"`
	Args          map[string]struct {
		Default string `yaml:"default"`
		Pattern string `yaml:"pattern"`
	} `yaml:"args"`
	Capabilities []kitCapability `yaml:"capabilities"`
}

func pythonExampleKit(t *testing.T) pythonExampleSpec {
	t.Helper()
	content := testutil.ReadFile(t, "../../examples/books/python/kits/python/python.yaml")
	var spec pythonExampleSpec
	if err := yaml.Unmarshal([]byte(content), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.SchemaVersion != "3" || spec.Kind != "mixin" || len(spec.Capabilities) != 2 {
		t.Fatalf("expected a v3 mixin with two capabilities: %+v", spec)
	}
	return spec
}

func pythonExampleCapability(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, capability := range pythonExampleKit(t).Capabilities {
		if capability.Type == name {
			return capability.Config
		}
	}
	t.Fatalf("missing capability %s", name)
	return nil
}

func TestThePythonBookNamesItselfAndItsKit(t *testing.T) {
	book := pythonExampleBook(t)
	if book.Name != "python" || !reflect.DeepEqual(book.kits, []any{"./kits/python"}) {
		t.Fatalf("book = %+v", book)
	}
}

func exampleAllowedHosts(t *testing.T, entries any) []string {
	t.Helper()
	var hosts []string
	for _, entry := range entries.([]any) {
		switch entry := entry.(type) {
		case string:
			hosts = append(hosts, entry)
		case map[string]any:
			for _, host := range entry["hosts"].([]any) {
				hosts = append(hosts, host.(string))
			}
		default:
			t.Fatalf("unexpected network entry %v", entry)
		}
	}
	return hosts
}

func TestThePythonKitAllowsPyPIAtInstallAndRuntimeAndNothingElseAtRuntime(t *testing.T) {
	network := pythonExampleCapability(t, "com.docker.sandbox/network-policy@2")
	for phase, want := range map[string][]string{
		"runtime": {"files.pythonhosted.org", "pypi.org"},
		"install": {"files.pythonhosted.org", "github.com:443", "pypi.org", "release-assets.githubusercontent.com:443"},
	} {
		rules := network[phase].(map[string]any)
		got := exampleAllowedHosts(t, rules["allow"])
		slices.Sort(got)
		if !reflect.DeepEqual(got, want) || len(ruleHosts(rules["deny"])) != 0 {
			t.Errorf("%s rules = %v, want only %v", phase, rules, want)
		}
		for _, host := range got {
			if strings.ContainsAny(host, "*?[]") {
				t.Errorf("host is a pattern: %s", host)
			}
		}
	}
	install := network["install"].(map[string]any)["allow"].([]any)
	var bounded []string
	for _, entry := range install {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hosts := exampleAllowedHosts(t, []any{rule})
		bounded = append(bounded, hosts...)
		if !reflect.DeepEqual(rule["methods"], []any{"GET"}) {
			t.Errorf("download rule must grant only GET: %v", rule)
		}
		if slices.Contains(hosts, "github.com:443") && !reflect.DeepEqual(rule["paths"], []any{"/astral-sh/python-build-standalone/releases/download/**"}) {
			t.Errorf("GitHub downloads must be scoped to Python builds: %v", rule)
		}
	}
	slices.Sort(bounded)
	if !reflect.DeepEqual(bounded, []string{"github.com:443", "release-assets.githubusercontent.com:443"}) {
		t.Errorf("expected bounded download hosts, got %v", bounded)
	}
}

func TestThePythonKitsPythonArgumentAcceptsVersionsAndRefusesAnythingElse(t *testing.T) {
	spec := pythonExampleKit(t)
	if len(spec.Args) != 1 || spec.Args["python"].Default != "3.12" {
		t.Fatalf("args = %v", spec.Args)
	}
	argument := spec.Args["python"]
	pattern, err := regexp.Compile(argument.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{argument.Default, "3.11", "3.12.4"} {
		if !pattern.MatchString(version) {
			t.Errorf("refused version %q", version)
		}
	}
	for _, version := range []string{"", "3", "3.12.", "latest", "3.12-dev", "3.12; id", "3.12\n", "$(id)", " 3.12", "３.１２"} {
		if pattern.MatchString(version) {
			t.Errorf("accepted invalid version %q", version)
		}
	}
}

func TestThePythonBooksAndKitsDescriptionsNameEveryHostTheKitAllows(t *testing.T) {
	book, spec := pythonExampleBook(t), pythonExampleKit(t)
	network := pythonExampleCapability(t, "com.docker.sandbox/network-policy@2")
	for _, phase := range []string{"install", "runtime"} {
		for _, host := range exampleAllowedHosts(t, network[phase].(map[string]any)["allow"]) {
			host = strings.TrimSuffix(host, ":443")
			if !strings.Contains(book.Description, host) || !strings.Contains(spec.Description, host) {
				t.Errorf("descriptions must name %s", host)
			}
		}
	}
}

func TestThePythonKitsInstallCommandsUseOnlyThePinnedArgumentAndInstallUvFromPyPI(t *testing.T) {
	lifecycle := pythonExampleCapability(t, "com.docker.sandbox/lifecycle@1")
	steps, ok := lifecycle["install"].([]any)
	if !ok || len(steps) == 0 || len(lifecycle) != 1 {
		t.Fatalf("expected only install hooks: %v", lifecycle)
	}
	var commands string
	for _, step := range steps {
		step := step.(map[string]any)
		if step["user"] != "0" || step["description"] == nil || step["description"] == "" {
			t.Errorf("install needs root and a description: %v", step)
		}
		commands += step["command"].(string) + "\n"
	}
	for _, forbidden := range []string{"curl", "wget", "apt", "|"} {
		if strings.Contains(commands, forbidden) {
			t.Errorf("unexpected installer %q", forbidden)
		}
	}
	refs := regexp.MustCompile(`\$\{\{\s*kit\.args\.[^}]+\}\}`).FindAllString(commands, -1)
	if !reflect.DeepEqual(refs, []string{"${{ kit.args.python }}"}) {
		t.Errorf("argument references = %v", refs)
	}
	for _, required := range []string{"python3 -m venv", "pip install --no-cache-dir uv", "UV_NO_CACHE=1", "uv python install", "UV_PYTHON_INSTALL_DIR=/usr/local/share/uv/python", "UV_PYTHON_BIN_DIR=/usr/local/bin"} {
		if !strings.Contains(commands, required) {
			t.Errorf("install lacks %q", required)
		}
	}
}
