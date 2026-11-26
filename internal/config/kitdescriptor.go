package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const kitFilesDir = "files"

var (
	inertKitNames  = []string{"readme", "license", "notice", "changelog", "authors", "contributing"}
	kitCommentLine = regexp.MustCompile(`(?im)^[ \t]*#[ \t]*kit[ \t]*:`)
	allowedAtRoot  = "a kit's root may hold one descriptor (.yaml, .yml or .json), README, LICENSE, NOTICE, CHANGELOG, AUTHORS, " +
		"CONTRIBUTING (bare or .md or .txt), .gitignore and a files/ directory; everything else belongs under files/"
)

func inertKitFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".gitignore" {
		return true
	}
	stem, extension := lower, ""
	if dot := strings.LastIndex(lower, "."); dot > 0 {
		stem, extension = lower[:dot], lower[dot:]
	}
	return slices.Contains(inertKitNames, stem) && slices.Contains([]string{"", ".md", ".txt"}, extension)
}

func kitRoot(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errorf("cannot list the kit: %v", err)
	}
	var descriptors, names []string
	for _, entry := range entries {
		name := entry.Name()
		shown := Printable(name)
		path := filepath.Join(dir, name)
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return nil, errorf("%s is a link, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case entry.IsDir() && name == kitFilesDir:
			continue
		case entry.IsDir():
			return nil, errorf("%s is a directory, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case !entry.Type().IsRegular():
			return nil, errorf("%s is not a regular file, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case contains(kitFileExtensions, strings.ToLower(filepath.Ext(name))):
			descriptors, names = append(descriptors, path), append(names, shown)
		case !inertKitFile(name):
			return nil, errorf("%s is not allowed at the kit's root; %s", shown, allowedAtRoot)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, errorf("%s cannot be read: %v", shown, err)
		}
		file.Close()
	}
	switch len(descriptors) {
	case 0:
		return nil, errorf("has no descriptor; a kit has exactly one (.yaml, .yml or .json) at its root")
	case 1:
		return descriptors, nil
	}
	return nil, errorf("has %d descriptors (%s); a kit has exactly one", len(descriptors), strings.Join(names, ", "))
}
