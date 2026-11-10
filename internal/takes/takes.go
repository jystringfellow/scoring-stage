// Package takes writes the files a sandbox hands over into a directory on the host.
package takes

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Limits struct {
	Bytes   int64
	Entries int
}

var DefaultLimits = Limits{Bytes: 1 << 30, Entries: 100_000}

type Summary struct {
	Files   int
	Bytes   int64
	Skipped int
}

// Extract writes the archive's regular files and directories under dir and
// leaves links and special files behind. The archive comes from a sandbox, so
// an entry that would land outside dir is an error.
func Extract(archive io.Reader, dir string, limits Limits) (Summary, error) {
	var summary Summary
	reader := tar.NewReader(archive)
	for entries := 1; ; entries++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return summary, nil
		}
		if err != nil {
			return summary, fmt.Errorf("cannot read the take: %w", err)
		}
		if entries > limits.Entries {
			return summary, fmt.Errorf("the take has more than %d entries", limits.Entries)
		}
		name, err := inside(header.Name)
		if err != nil {
			return summary, err
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return summary, err
			}
		case tar.TypeReg:
			if summary.Bytes+header.Size > limits.Bytes {
				return summary, fmt.Errorf("the take is larger than %d bytes", limits.Bytes)
			}
			if err := writeFile(target, reader, header.Size); err != nil {
				return summary, err
			}
			summary.Files++
			summary.Bytes += header.Size
		default:
			summary.Skipped++
		}
	}
}

func inside(name string) (string, error) {
	cleaned := path.Clean(name)
	if path.IsAbs(name) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("the take holds %q, which is outside the take", name)
	}
	return cleaned, nil
}

func writeFile(target string, content io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, content, size)
	return errors.Join(copyErr, file.Close())
}
