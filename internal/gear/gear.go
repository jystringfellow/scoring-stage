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
	hostPath string
	name     string
	mode     int64
	dir      bool
	size     int64
	modified time.Time
}

type Source struct {
	config.Gear
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

func Look(gear []config.Gear, limits Limits) (Survey, error) {
	looking := &survey{limits: limits}
	for _, item := range gear {
		info, err := os.Lstat(item.HostPath)
		switch {
		case errors.Is(err, fs.ErrNotExist), err == nil && info.Mode()&fs.ModeSymlink != 0:
			looking.Missing = append(looking.Missing, item)
			continue
		case err != nil:
			return looking.Survey, err
		case !info.IsDir() && !info.Mode().IsRegular():
			return looking.Survey, fmt.Errorf("%s is neither a file nor a directory", item.From)
		}
		source := Source{Gear: item}
		base := path.Base(item.To)
		err = filepath.WalkDir(item.HostPath, func(hostPath string, found fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !found.IsDir() && !found.Type().IsRegular() {
				looking.Skipped++
				return nil
			}
			info, err := found.Info()
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(item.HostPath, hostPath)
			if err != nil {
				return err
			}
			added := entry{hostPath: hostPath, name: path.Join(base, filepath.ToSlash(relative)), modified: info.ModTime()}
			if found.IsDir() {
				added.dir, added.mode = true, 0o700
			} else {
				added.size, added.mode = info.Size(), int64(info.Mode().Perm()&0o700)
			}
			if err := looking.count(added); err != nil {
				return err
			}
			source.entries = append(source.entries, added)
			return nil
		})
		if err != nil {
			return looking.Survey, err
		}
		looking.Sources = append(looking.Sources, source)
	}
	return looking.Survey, nil
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
		if err := entry.write(writer); err != nil {
			return err
		}
	}
	return writer.Close()
}

func (e entry) write(writer *tar.Writer) error {
	changed := fmt.Errorf("%s changed while stage was copying it", e.hostPath)
	file, err := os.OpenFile(e.hostPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) || errors.Is(err, fs.ErrNotExist) {
		return changed
	}
	if err != nil {
		return err
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
