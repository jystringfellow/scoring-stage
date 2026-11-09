package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const (
	bindingExpected      = "expected"
	bindingStale         = "stale"
	bindingNotConfigured = "stage (not configured)"
	bindingOther         = "other"
	bindingUnrecognised  = "unrecognised entry"
)

func (a *App) auditBindings(expected map[string]*config.StageBinding) {
	path := sbx.CredentialsPath()
	if path == "" {
		return
	}
	bindings, err := sbx.ReadBindings(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		a.say("global credential bindings: not found at %s (path inferred from sbx; only $HOME/.config verified)", printable(path))
		return
	case err != nil:
		a.warn("cannot audit global credential bindings: %s", printable(err.Error()))
		return
	case len(bindings) == 0:
		a.say("global credential bindings: none in %s", printable(path))
		return
	}
	secrets, secretsErr := sbx.GlobalSecrets()
	a.say("global credential bindings in %s; sbx keeps them after sandboxes are gone. Any kit that declares credential@1 for a bound service can send "+
		"that service's secret to any domain the kit names; outside stage sandboxes a binding is inert unless its service has a global secret:", printable(path))
	if secretsErr != nil {
		a.warn("cannot tell which bound services have a global secret: %s", printable(secretsErr.Error()))
	}
	known := config.KnownStageBindings()
	for _, binding := range bindings {
		mark, reference, extra := classifyBinding(binding, expected, known)
		global := slices.Contains(secrets, binding.Service)
		label := mark
		if global {
			label += ", global secret set"
		}
		fmt.Fprintf(a.Stdout, "  %s (%s): %s\n", printable(binding.Service), label, printable(strings.Join(binding.Domains, ", ")))
		fmt.Fprintf(a.Stdout, "    %s\n", bindingGuidance(binding, mark, global, extra, reference, path))
	}
}

func classifyBinding(binding sbx.Binding, expected, known map[string]*config.StageBinding) (string, *config.StageBinding, []string) {
	if binding.Unrecognised {
		return bindingUnrecognised, nil, nil
	}
	mark, reference := bindingExpected, expected[binding.Service]
	if reference == nil {
		mark, reference = bindingNotConfigured, known[binding.Service]
	}
	if reference == nil {
		return bindingOther, nil, nil
	}
	var extra []string
	for _, domain := range binding.Domains {
		if !containsFold(reference.Domains, domain) {
			extra = append(extra, domain)
		}
	}
	if len(extra) > 0 {
		return bindingStale, reference, extra
	}
	return mark, reference, nil
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func bindingGuidance(binding sbx.Binding, mark string, global bool, extra []string, reference *config.StageBinding, path string) string {
	service := printable(binding.Service)
	file := printable(path)
	switch mark {
	case bindingUnrecognised:
		return fmt.Sprintf("stage cannot read this entry's domains; inspect the %s entry in %s", service, file)
	case bindingExpected:
		return fmt.Sprintf("stage binds this for %s; run only kits you trust with the %s credential", strings.Join(reference.Reasons, ", "), service)
	case bindingNotConfigured:
		return fmt.Sprintf("stage binds this when you use %s; remove it only if you no longer do", strings.Join(reference.Reasons, " or "))
	case bindingStale:
		return fmt.Sprintf("stage binds only %s for %s; sbx never drops a binding domain, so delete %s from the %s entry in %s by hand",
			strings.Join(reference.Domains, ", "), strings.Join(reference.Reasons, ", "), printable(strings.Join(extra, ", ")), service, file)
	}
	remove := fmt.Sprintf("if nothing needs it, delete the %s entry from %s by hand", service, file)
	prune := "(never `sbx env rm --prune-bindings` a stage sandbox: it removes shared entries machine-wide)"
	if global {
		return fmt.Sprintf("`sbx secret rm %s` removes the global secret any kit can send anywhere; stage does not bind this, so %s %s",
			printable(config.ShellQuote(binding.Service)), remove, prune)
	}
	return fmt.Sprintf("stage does not bind this; %s %s", remove, prune)
}
