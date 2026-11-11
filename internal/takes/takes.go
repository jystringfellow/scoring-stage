// Package takes writes the files a sandbox hands over into a directory on the host.
package takes

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

// Entries bounds both the archive's entries and the files and directories
// created; Stream bounds everything the sandbox sends, kept or not.
type Limits struct {
	Bytes   int64
	Entries int
	Stream  int64
}

var DefaultLimits = Limits{Bytes: 1 << 30, Entries: 100_000, Stream: 2 << 30}

const longestName = 4096

type Summary struct {
	Files   int
	Bytes   int64
	Skipped int
}

type extraction struct {
	dir     string
	limits  Limits
	known   map[string]bool
	written map[string]bool
	created int
}

// Extract reads the archive to its end. The archive comes from a sandbox, so
// an entry that would land outside dir is an error. Overlapping takes repeat
// a file, and only its first copy is kept.
func Extract(archive io.Reader, dir string, limits Limits) (Summary, error) {
	var summary Summary
	stream := &bounded{reader: archive, limit: limits.Stream, left: limits.Stream}
	extraction := &extraction{dir: dir, limits: limits, known: map[string]bool{}, written: map[string]bool{}}
	reader := tar.NewReader(stream)
	for entries := 1; ; entries++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			if _, err := io.Copy(io.Discard, stream); err != nil {
				return summary, fmt.Errorf("cannot read the take: %w", err)
			}
			return summary, nil
		}
		if err != nil {
			return summary, fmt.Errorf("cannot read the take: %w", err)
		}
		if entries > limits.Entries {
			return summary, extraction.tooMany()
		}
		name, err := inside(header.Name)
		if err != nil {
			return summary, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := extraction.makeDirs(name); err != nil {
				return summary, err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > limits.Bytes-summary.Bytes {
				return summary, fmt.Errorf("the take is larger than %d bytes", limits.Bytes)
			}
			if extraction.written[name] {
				continue
			}
			if err := extraction.writeFile(name, reader, header.Size); err != nil {
				return summary, err
			}
			extraction.written[name] = true
			summary.Files++
			summary.Bytes += header.Size
		default:
			summary.Skipped++
		}
	}
}

func inside(name string) (string, error) {
	if len(name) > longestName {
		return "", fmt.Errorf("the take holds a name of %d bytes; the longest allowed is %d", len(name), longestName)
	}
	if !utf8.ValidString(name) || config.HasControl(name) {
		return "", fmt.Errorf("the take holds %s, a name with control characters or invalid UTF-8", config.Printable(name))
	}
	cleaned := path.Clean(name)
	if path.IsAbs(name) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("the take holds %q, which is outside the take", name)
	}
	return cleaned, nil
}

func (e *extraction) tooMany() error {
	return fmt.Errorf("the take has more than %d entries", e.limits.Entries)
}

func (e *extraction) count() error {
	if e.created++; e.created > e.limits.Entries {
		return e.tooMany()
	}
	return nil
}

func (e *extraction) makeDirs(name string) error {
	if name == "." || e.known[name] {
		return nil
	}
	if err := e.makeDirs(path.Dir(name)); err != nil {
		return err
	}
	if err := e.count(); err != nil {
		return err
	}
	err := os.Mkdir(filepath.Join(e.dir, filepath.FromSlash(name)), 0o700)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	e.known[name] = true
	return nil
}

func (e *extraction) writeFile(name string, content io.Reader, size int64) error {
	if err := e.makeDirs(path.Dir(name)); err != nil {
		return err
	}
	if err := e.count(); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(e.dir, filepath.FromSlash(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, content, size)
	return errors.Join(copyErr, file.Close())
}

type bounded struct {
	reader      io.Reader
	limit, left int64
}

func (b *bounded) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var next [1]byte
		if n, err := b.reader.Read(next[:]); n == 0 {
			return 0, err
		}
		return 0, fmt.Errorf("the sandbox sent more than %d bytes", b.limit)
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.reader.Read(p)
	b.left -= int64(n)
	return n, err
}
