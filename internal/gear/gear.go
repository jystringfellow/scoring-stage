package gear

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

type Limits struct {
	Bytes   int64
	Entries int
}

var DefaultLimits = Limits{Bytes: 100 << 20, Entries: 10_000}

type entry struct {
	inRoot   string
	hostPath string
	name     string
	mode     int64
	dir      bool
	size     int64
	modified time.Time
}

type Source struct {
	config.Gear
	root    *os.Root
	entries []entry
}

type Survey struct {
	Sources []Source
	Missing []config.Gear
	Files   int
	Bytes   int64
	Skipped int
}

type survey struct {
	Survey
	limits  Limits
	entries int
}

const readOnlyNeverFollowing = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// Look holds each source's directory open until Close, so that Archive reads
// the files it measured even if a path to them is replaced in between.
func Look(gear []config.Gear, limits Limits) (Survey, error) {
	looking := &survey{limits: limits}
	for _, item := range gear {
		info, err := os.Lstat(item.HostPath)
		switch {
		case errors.Is(err, fs.ErrNotExist), err == nil && info.Mode()&fs.ModeSymlink != 0:
			looking.Missing = append(looking.Missing, item)
			continue
		case err != nil:
			looking.Close()
			return looking.Survey, err
		case !info.IsDir() && !info.Mode().IsRegular():
			looking.Close()
			return looking.Survey, fmt.Errorf("%s is neither a file nor a directory", item.From)
		}
		source, err := looking.add(item, info.IsDir())
		if err != nil {
			looking.Close()
			return looking.Survey, err
		}
		looking.Sources = append(looking.Sources, source)
	}
	return looking.Survey, nil
}

func (s *survey) add(item config.Gear, isDir bool) (Source, error) {
	held, start := filepath.Dir(item.HostPath), filepath.Base(item.HostPath)
	if isDir {
		held, start = item.HostPath, "."
	}
	root, err := os.OpenRoot(held)
	if err != nil {
		return Source{}, err
	}
	source := Source{Gear: item, root: root}
	base := path.Base(item.To)
	err = fs.WalkDir(root.FS(), start, func(inRoot string, found fs.DirEntry, err error) error {
		hostPath := filepath.Join(held, filepath.FromSlash(inRoot))
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", hostPath, err)
		}
		if !found.IsDir() && !found.Type().IsRegular() {
			s.Skipped++
			return nil
		}
		info, err := found.Info()
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", hostPath, err)
		}
		added := entry{inRoot: inRoot, hostPath: hostPath, name: base, modified: info.ModTime()}
		if isDir {
			added.name = path.Join(base, inRoot)
		}
		if found.IsDir() {
			added.dir, added.mode = true, 0o700
		} else {
			added.size, added.mode = info.Size(), int64(info.Mode().Perm()&0o700)
			file, err := root.OpenFile(inRoot, readOnlyNeverFollowing, 0)
			if err != nil {
				return fmt.Errorf("cannot read %s: %w", hostPath, err)
			}
			file.Close()
		}
		if err := s.count(added); err != nil {
			return err
		}
		source.entries = append(source.entries, added)
		return nil
	})
	if err != nil {
		root.Close()
		return Source{}, err
	}
	return source, nil
}

func (s Survey) Close() {
	for _, source := range s.Sources {
		source.root.Close()
	}
}

func (s *survey) count(added entry) error {
	if s.entries++; s.entries > s.limits.Entries {
		return fmt.Errorf("the gear has more than %d files and directories", s.limits.Entries)
	}
	if added.dir {
		return nil
	}
	if added.size > s.limits.Bytes-s.Bytes {
		return fmt.Errorf("the gear is larger than %d bytes", s.limits.Bytes)
	}
	s.Files++
	s.Bytes += added.size
	return nil
}

func (s Source) Archive(to io.Writer) error {
	writer := tar.NewWriter(to)
	for _, entry := range s.entries {
		if entry.dir {
			if err := writer.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: entry.name + "/", Mode: entry.mode, ModTime: entry.modified}); err != nil {
				return err
			}
			continue
		}
		if err := entry.write(s.root, writer); err != nil {
			return err
		}
	}
	return writer.Close()
}

func (e entry) write(root *os.Root, writer *tar.Writer) error {
	changed := fmt.Errorf("%s changed while stage was copying it", e.hostPath)
	file, err := root.OpenFile(e.inRoot, readOnlyNeverFollowing, 0)
	if err != nil {
		return fmt.Errorf("%w: %v", changed, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != e.size {
		return changed
	}
	if err := writer.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: e.mode, Size: e.size, ModTime: e.modified}); err != nil {
		return err
	}
	_, err = io.CopyN(writer, file, e.size)
	if errors.Is(err, io.EOF) {
		return changed
	}
	return err
}
