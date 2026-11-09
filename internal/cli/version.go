package cli

import "runtime/debug"

var Version = "dev"

func versionOf(linked string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if linked != "dev" && linked != "" {
		return linked
	}
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
