package config

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const kitFilesDir = "files"

var (
	inertKitNames   = []string{"readme", "license", "notice", "changelog", "authors", "contributing"}
	inertExactNames = []string{".gitignore", ".gitattributes", ".gitkeep", ".editorconfig", ".ds_store", "thumbs.db"}
	commentOpening  = regexp.MustCompile(`^#+[ \t\x{00A0}]*(?i:kit):[ \t\x{00A0}]*$`)
	descriptorLine  = regexp.MustCompile(`\r\n|\r|\n`)
	vimSwapFile     = regexp.MustCompile(`^\..+\.sw[a-p]$`)
	allowedAtRoot   = "a kit's root may hold one descriptor (.yaml, .yml or .json), README, LICENSE, NOTICE, CHANGELOG, AUTHORS, " +
		"CONTRIBUTING (bare or .md or .txt), .gitignore, .gitattributes, .gitkeep, .editorconfig, .DS_Store, Thumbs.db and a files/ directory; " +
		"everything else belongs under files/"
)

func asciiLower(name string) string {
	lowered := []byte(name)
	for i, b := range lowered {
		if 'A' <= b && b <= 'Z' {
			lowered[i] = b + 'a' - 'A'
		}
	}
	return string(lowered)
}

func inertKitFile(name string) bool {
	lower := asciiLower(name)
	if slices.Contains(inertExactNames, lower) {
		return true
	}
	stem, extension := lower, ""
	if dot := strings.LastIndex(lower, "."); dot > 0 {
		stem, extension = lower[:dot], lower[dot:]
	}
	return slices.Contains(inertKitNames, stem) && slices.Contains([]string{"", ".md", ".txt"}, extension)
}

func editorOrSystemFile(name string) bool {
	return strings.HasPrefix(name, "._") || strings.HasSuffix(name, "~") ||
		len(name) > 1 && strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#") || vimSwapFile.MatchString(name)
}

func opensCommentDescriptor(content []byte) bool {
	for _, line := range descriptorLine.Split(string(bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})), -1) {
		if commentOpening.MatchString(line) {
			return true
		}
	}
	return false
}

func kitRoot(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errorf("cannot list the kit: %s", Printable(err.Error()))
	}
	var descriptors, names []string
	for _, entry := range entries {
		name := entry.Name()
		shown := Printable(name)
		path := filepath.Join(dir, name)
		switch {
		case !isASCII(name):
			return nil, errorf("%s is not an ASCII name, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case entry.Type()&os.ModeSymlink != 0:
			return nil, errorf("%s is a link, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case entry.IsDir() && name == kitFilesDir:
			continue
		case entry.IsDir():
			return nil, errorf("%s is a directory, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case !entry.Type().IsRegular():
			return nil, errorf("%s is not a regular file, which is not allowed at the kit's root; %s", shown, allowedAtRoot)
		case editorOrSystemFile(name):
			return nil, errorf("%s is an editor or system file at the kit's root; close the editor or delete the file", shown)
		case contains(kitFileExtensions, asciiLower(filepath.Ext(name))):
			descriptors, names = append(descriptors, path), append(names, shown)
		case !inertKitFile(name):
			return nil, errorf("%s is not allowed at the kit's root; %s", shown, allowedAtRoot)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, errorf("%s cannot be read: %s", shown, Printable(err.Error()))
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
