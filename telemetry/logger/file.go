package logger

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	// 日志时间戳格式化依赖本地时区数据库;嵌入 tzdata 使容器无需系统 tzdata。
	_ "time/tzdata" //nolint:revive // blank import embeds the tz database
)

// FileOptions controls size rotation. Pointer fields distinguish defaults from
// explicit zero/false. Zero count or age means unlimited retention.
type FileOptions struct {
	Path       string // Relative paths are resolved once when the runtime opens.
	MaxSizeMB  int    // Zero defaults to 100 MiB.
	MaxBackups *int   // Nil defaults to 10; zero means unlimited.
	MaxAgeDays *int   // Nil defaults to 7; zero means unlimited.
	Compress   *bool  // Nil defaults to true.
	Timezone   string // Empty defaults to UTC.
}
type fileWriter struct {
	path           string
	max            int64
	backups, age   int
	compress       bool
	compressFn     func(string) error
	maintenance    *fileMaintenance
	location       *time.Location
	f              *os.File
	size           int64
	archivePattern *regexp.Regexp
	tempPattern    *regexp.Regexp
}

func openFile(o FileOptions) (*fileWriter, error) {
	if strings.TrimSpace(o.Path) == "" {
		return nil, errors.New("empty log path")
	}
	if o.MaxSizeMB < 0 || int64(o.MaxSizeMB) > math.MaxInt64/(1024*1024) {
		return nil, errors.New("invalid maximum size")
	}
	if o.MaxSizeMB == 0 {
		o.MaxSizeMB = 100
	}
	path, err := filepath.Abs(o.Path)
	if err != nil {
		return nil, err
	}
	w := &fileWriter{path: path, max: int64(o.MaxSizeMB) * 1024 * 1024, backups: 10, age: 7, compress: true, compressFn: compressArchive}
	if o.MaxBackups != nil {
		w.backups = *o.MaxBackups
	}
	if o.MaxAgeDays != nil {
		w.age = *o.MaxAgeDays
	}
	if o.Compress != nil {
		w.compress = *o.Compress
	}
	if w.backups < 0 || w.age < 0 {
		return nil, errors.New("negative retention limit")
	}
	zone := o.Timezone
	if zone == "" {
		zone = "UTC"
	}
	var e error
	w.location, e = time.LoadLocation(zone)
	if e != nil {
		return nil, e
	}
	w.archivePattern, w.tempPattern = archivePatterns(filepath.Base(w.path))
	if e = os.MkdirAll(filepath.Dir(w.path), 0750); e != nil {
		return nil, e
	}
	if e = w.open(); e != nil {
		return nil, e
	}
	if e = w.maintain(); e != nil {
		return nil, errors.Join(e, w.close())
	}
	return w, nil
}
func (w *fileWriter) open() error {
	// Avoid blocking on existing FIFOs/devices in operator-controlled directories.
	// Retain the post-open check; this is not a race-proof security boundary.
	if info, err := os.Stat(w.path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("log path is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, e := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if e != nil {
		return e
	}
	s, e := f.Stat()
	if e != nil {
		return errors.Join(e, f.Close())
	}
	if !s.Mode().IsRegular() {
		return errors.Join(errors.New("log path is not a regular file"), f.Close())
	}
	w.f = f
	w.size = s.Size()
	return nil
}
func (w *fileWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max {
		return 0, fmt.Errorf("log record exceeds %d bytes", w.max)
	}
	var maintenance error
	if w.f == nil {
		maintenance = w.reopen()
		if w.f == nil {
			return 0, maintenance
		}
	}
	if w.size+int64(len(p)) > w.max {
		maintenance = errors.Join(maintenance, w.rotate())
		if w.f == nil || w.size+int64(len(p)) > w.max {
			return 0, maintenance
		}
	}
	n, e := w.f.Write(p)
	w.size += int64(n)
	return n, errors.Join(maintenance, e)
}
func (w *fileWriter) close() error {
	if w.f == nil {
		return nil
	}
	e := w.f.Close()
	w.f = nil
	return e
}
func (w *fileWriter) rotate() error {
	if w.f == nil {
		if err := w.reopen(); err != nil {
			return err
		}
	}
	if w.size == 0 {
		return w.scheduleMaintenance("")
	}
	// CreateTemp reserves a unique name across rapid rotations and restarts.
	stamp := time.Now().In(w.location).Format("20060102T150405.000000000Z0700")
	base := filepath.Base(w.path)
	reservation := "." + base + ".logger-reserve-"
	tmp, e := os.CreateTemp(filepath.Dir(w.path), reservation+"*")
	if e != nil {
		return e
	}
	suffix := strings.TrimPrefix(filepath.Base(tmp.Name()), reservation)
	archive := filepath.Join(filepath.Dir(w.path), base+".archive-"+stamp+"-"+suffix)
	if e = tmp.Close(); e != nil {
		return e
	}
	if e = os.Remove(tmp.Name()); e != nil {
		return e
	}
	if e = w.close(); e != nil {
		return e
	}
	if w.maintenance != nil {
		w.maintenance.mu.Lock()
		w.maintenance.pending[archive] = true
	}
	if e = os.Rename(w.path, archive); e != nil {
		if w.maintenance != nil {
			delete(w.maintenance.pending, archive)
			w.maintenance.mu.Unlock()
		}
		return errors.Join(e, w.open())
	}
	if w.maintenance != nil {
		w.maintenance.mu.Unlock()
	}
	e = w.open()
	return errors.Join(e, w.scheduleMaintenance(archive))
}

func (w *fileWriter) finishRotation(archive string) error {
	var ce error
	if w.compress {
		ce = w.compressFn(archive)
	}
	// Retention is independent of compression and may remove an original that
	// exceeds count/age limits even when compression failed.
	return errors.Join(ce, w.maintain())
}

// reopen also completes pending retention after a transient rotation failure.
func (w *fileWriter) reopen() error {
	if err := w.open(); err != nil {
		return err
	}
	return w.scheduleMaintenance("")
}
