package cli

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const secretSetTimeout = 2 * time.Minute

type envSecret struct {
	Command string `yaml:"command"`
	Refresh string `yaml:"refresh"`
}

func (a *App) cmdSecrets(args []string, home string) (int, error) {
	positional, err := a.newFlags("secrets", "refresh <slug>").parse(args, "action", "slug")
	if err != nil {
		return 0, err
	}
	if positional[0] != "refresh" {
		return 0, &usageError{"argument action: invalid choice: " + config.Printable(positional[0]) + " (choose from refresh)"}
	}
	slug := positional[1]
	task, err := loadTask(home, slug)
	if err != nil {
		return 0, err
	}
	content, err := os.ReadFile(filepath.Join(taskDir(home, slug), "sbxenv.yaml"))
	if err != nil {
		return 0, err
	}
	var env struct {
		Secrets map[string]envSecret `yaml:"secrets"`
	}
	if err := yaml.Unmarshal(content, &env); err != nil {
		return 0, errorf("cannot read the env file of %s: %v", slug, err)
	}
	if len(env.Secrets) == 0 {
		a.say("%s has no secrets to refresh", task.Sandbox)
		return 0, nil
	}
	var left []string
	for _, service := range slices.Sorted(maps.Keys(env.Secrets)) {
		secret := env.Secrets[service]
		if !config.IsSbxService(service) || secret.Command == "" {
			left = append(left, service)
			continue
		}
		argv := []string{"secret", "set", service, "--sandbox", task.Sandbox, "--command", secret.Command}
		if secret.Refresh != "" {
			argv = append(argv, "--refresh", secret.Refresh)
		}
		argv = append(argv, "--force", "--no-input")
		if _, err := sbx.FeedWithin(secretSetTimeout, strings.NewReader(""), argv...); err != nil {
			return 0, errorf("could not set %s for %s: %v", service, task.Sandbox, err)
		}
		a.say("%s: set again for %s; sbx runs its command on the sandbox's next request that needs it", service, task.Sandbox)
	}
	if len(left) > 0 {
		a.warn("%s cannot be set again with sbx secret set; to give %s new values for them, run `stage down %s` and `stage up`",
			config.Printable(strings.Join(left, ", ")), task.Sandbox, slug)
	}
	return 0, nil
}
