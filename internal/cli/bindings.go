package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const (
	bindingExpected = "expected"
	bindingStale    = "stale"
	bindingOther    = "other"
)

func (a *App) auditBindings(expected map[string]*config.StageBinding) {
	path := sbx.CredentialsPath()
	if path == "" {
		return
	}
	bindings, err := sbx.ReadBindings(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		a.say("global credential bindings: none (%s does not exist)", printable(path))
		return
	case err != nil:
		a.warn("cannot audit global credential bindings: %s", printable(err.Error()))
		return
	case len(bindings) == 0:
		a.say("global credential bindings: none in %s", printable(path))
		return
	}
	a.say("global credential bindings in %s; sbx keeps them after sandboxes are gone, and any kit that declares credential@1 for a bound service can send its secret to any domain the kit names:", printable(path))
	for _, binding := range bindings {
		mark, extra := classifyBinding(binding, expected[binding.Service])
		fmt.Fprintf(a.Stdout, "  %s (%s): %s\n", printable(binding.Service), mark, printable(strings.Join(binding.Domains, ", ")))
		fmt.Fprintf(a.Stdout, "    %s\n", bindingGuidance(binding, mark, extra, expected[binding.Service], path))
	}
}

func classifyBinding(binding sbx.Binding, stage *config.StageBinding) (string, []string) {
	if stage == nil {
		return bindingOther, nil
	}
	var extra []string
	for _, domain := range binding.Domains {
		if !containsFold(stage.Domains, domain) {
			extra = append(extra, domain)
		}
	}
	if len(extra) > 0 {
		return bindingStale, extra
	}
	return bindingExpected, nil
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func bindingGuidance(binding sbx.Binding, mark string, extra []string, stage *config.StageBinding, path string) string {
	service := printable(binding.Service)
	file := printable(path)
	switch mark {
	case bindingExpected:
		return fmt.Sprintf("stage binds this for %s; run only kits you trust with the %s credential", strings.Join(stage.Reasons, ", "), service)
	case bindingStale:
		return fmt.Sprintf("stage binds only %s for %s; sbx never drops a binding domain, so delete %s from the %s entry in %s by hand",
			strings.Join(stage.Domains, ", "), strings.Join(stage.Reasons, ", "), printable(strings.Join(extra, ", ")), service, file)
	default:
		return fmt.Sprintf("stage does not bind this; if nothing needs it, delete the %s entry from %s by hand, or `sbx secret rm %s` to drop the global secret it injects "+
			"(never `sbx env rm --prune-bindings` a stage sandbox: it removes shared entries machine-wide)", service, file, printable(config.ShellQuote(binding.Service)))
	}
}
