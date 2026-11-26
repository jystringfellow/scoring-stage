package config

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

const directivePrefixLimit = 64 << 10

var (
	parserDirective = regexp.MustCompile(`^(?:#|//|;)\s*([A-Za-z][A-Za-z0-9_-]*)\s*=\s*(.*?)\s*$`)
	lineBreak       = regexp.MustCompile(`\r\n|\r|\n`)
)

func refuseUnreadDescriptors(dir string) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(dir, path)
		underFiles := strings.HasPrefix(relative, "files"+string(filepath.Separator))
		if !underFiles && contains(kitFileExtensions, strings.ToLower(filepath.Ext(path))) {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if frontend, named := kitFrontendNamedBy(path); named {
			return errorf("kit %s: %s names the sandbox-kit frontend in its parser directives (syntax=%s), so it is a kit descriptor stage cannot read; "+
				"write the kit's descriptor as YAML", dir, relative, Printable(frontend))
		}
		return nil
	})
}

func kitFrontendNamedBy(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	prefix, _ := io.ReadAll(io.LimitReader(file, directivePrefixLimit))
	for _, line := range lineBreak.Split(decodedText(prefix), -1) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		directive := parserDirective.FindStringSubmatch(line)
		if directive == nil {
			return "", false
		}
		if strings.EqualFold(directive[1], "syntax") && strings.Contains(strings.ToLower(directive[2]), "sandbox-kit") {
			return directive[2], true
		}
	}
	return "", false
}

func decodedText(data []byte) string {
	littleEndian := bytes.HasPrefix(data, []byte{0xff, 0xfe})
	if !littleEndian && !bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		return string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 2; i+1 < len(data); i += 2 {
		if littleEndian {
			units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
		} else {
			units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
		}
	}
	return string(utf16.Decode(units))
}
