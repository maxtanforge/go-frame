package logger

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

func compressArchive(path string) (err error) {
	in, e := os.Open(path)
	if e != nil {
		return e
	}
	// 成功路径在 Remove 前显式 Close 验证读尽；defer 只兜早退路径的句柄泄漏，
	// 已显式关闭后不再二次 Close（那会以 "file already closed" 污染结果）。
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, in.Close())
		}
	}()
	// Include the exact archive identity so a later owner of this log path can
	// remove interrupted intermediates without touching another runtime's files.
	out, e := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".logger-gzip-*")
	if e != nil {
		return e
	}
	tmp := out.Name()
	defer func() {
		if e := os.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}()
	z := gzip.NewWriter(out)
	_, copyErr := io.Copy(z, in)
	zipErr := z.Close()
	closeErr := out.Close()
	if e = errors.Join(copyErr, zipErr, closeErr); e != nil {
		return e
	}
	if e = os.Rename(tmp, path+".gz"); e != nil {
		return e
	}
	if e = in.Close(); e != nil {
		return e
	}
	closed = true
	return os.Remove(path)
}

// Compile once for an owned writer. The basename is literal, including any
// regexp metacharacters; maintenance must never match another log's archives.
func archivePatterns(base string) (*regexp.Regexp, *regexp.Regexp) {
	archiveName := regexp.QuoteMeta(base) + `\.archive-[0-9]{8}T[0-9]{6}\.[0-9]{9}(Z|[+-][0-9]{4})-[0-9]+`
	pattern := regexp.MustCompile("^" + archiveName + `(\.gz)?$`)
	tempPattern := regexp.MustCompile(`^\.` + archiveName + `\.logger-gzip-[0-9]+$`)
	return pattern, tempPattern
}

func (w *fileWriter) maintain() error {
	entries, e := os.ReadDir(filepath.Dir(w.path))
	if e != nil {
		return e
	}
	type archive struct {
		path     string
		modified time.Time
	}
	var files []archive
	var errs []error
	for _, entry := range entries {
		isTemp := w.tempPattern.MatchString(entry.Name())
		if (!w.archivePattern.MatchString(entry.Name()) && !isTemp) || entry.IsDir() {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			errs = append(errs, e)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if isTemp {
			// The single maintenance worker runs this outside compression. Under
			// the single-owner contract, every owned temp here is interrupted work.
			if e = os.Remove(filepath.Join(filepath.Dir(w.path), entry.Name())); e != nil {
				errs = append(errs, e)
			}
			continue
		}
		files = append(files, archive{filepath.Join(filepath.Dir(w.path), entry.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].path > files[j].path
		}
		return files[i].modified.After(files[j].modified)
	})
	now := time.Now()
	for i, f := range files {
		expired := w.age > 0 && now.Sub(f.modified).Hours()/24 > float64(w.age)
		if w.maintenance != nil && w.maintenance.pending[f.path] {
			continue
		}
		if expired || (w.backups > 0 && i >= w.backups) {
			if e = os.Remove(f.path); e != nil {
				errs = append(errs, e)
			}
		}
	}
	return errors.Join(errs...)
}
