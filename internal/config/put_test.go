package config

import "testing"

func TestPutAndGearRefuseTheSameSandboxPaths(t *testing.T) {
	userHome(t)
	settings, err := gearConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"~/notes", "/srv/file", "relative", "~/../x", "~/a//b", "~/x/", "~/\x1bx", "/", "~/.gemini/antigravity-cli/antigravity-oauth-token", "/home/agent/.gemini", "/workspace", "/workspace/a", "/home/agent/notes"} {
		_, entryErr := decodeGearEntry(map[string]any{"from": "~/notes", "to": to})
		if entryErr == nil {
			entryErr = refuseLoginDestination(to, settings.Agents)
		}
		refused := entryErr != nil || GearLandsIn(Gear{To: to}, "/workspace")
		if got := settings.CheckPutDestination(to, "/workspace"); (got != nil) != refused {
			t.Errorf("%q: put=%v, gear refused=%v", to, got, refused)
		}
	}
}
