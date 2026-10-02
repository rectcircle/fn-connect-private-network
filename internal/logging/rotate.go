package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	DefaultMaxSize = int64(10 << 20)
	DefaultBackups = 5
)

type RotatingWriter struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	file    *os.File
	size    int64
}

func Open(path string, maxSize int64, backups int) (*RotatingWriter, error) {
	if path == "" {
		return nil, fmt.Errorf("log path is required")
	}
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if backups <= 0 {
		backups = DefaultBackups
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("set log directory permissions: %w", err)
	}
	writer := &RotatingWriter{
		path:    path,
		maxSize: maxSize,
		backups: backups,
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *RotatingWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(payload)) > w.maxSize {
		if err := w.rotate(); err != nil {
			if w.file == nil {
				return 0, err
			}
			count, writeErr := w.file.Write(payload)
			w.size += int64(count)
			return count, errors.Join(err, writeErr)
		}
	}
	count, err := w.file.Write(payload)
	w.size += int64(count)
	return count, err
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) open() error {
	file, size, err := openLogFile(w.path)
	if err != nil {
		return err
	}
	w.file = file
	w.size = size
	return nil
}

func openLogFile(path string) (*os.File, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("set log file permissions: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("stat log file: %w", err)
	}
	return file, info.Size(), nil
}

func (w *RotatingWriter) rotate() error {
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := os.Remove(w.path + "." + strconv.Itoa(w.backups)); err != nil &&
		!os.IsNotExist(err) {
		return err
	}
	for index := w.backups - 1; index >= 1; index-- {
		source := w.path + "." + strconv.Itoa(index)
		target := w.path + "." + strconv.Itoa(index+1)
		if err := os.Rename(source, target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, size, err := openLogFile(w.path)
	if err != nil {
		return err
	}
	previous := w.file
	w.file = file
	w.size = size
	return previous.Close()
}

func Redact(value string) string {
	return model.SafeText(value)
}

// RedactError preserves HTTP failure context without URL credentials or queries.
func RedactError(err error) string {
	return model.ErrorDetail(err)
}

func ErrorAttrs(err error) []any {
	public := model.PublicError(err)
	if public == nil {
		return nil
	}
	return []any{
		"code", public.Code, "retryable", public.Retryable,
		"operation", public.Operation, "error", public.Message,
		"detail", public.Detail, "http_status", public.HTTPStatus,
		"remote_code", public.RemoteCode, "request_id", public.RequestID,
		"cause", RedactError(model.AsError(err).Cause),
	}
}

func RedactAttr(_ []string, attr slog.Attr) slog.Attr {
	if err, ok := attr.Value.Any().(error); ok {
		return slog.String(attr.Key, model.FormatError(err))
	}
	if attr.Value.Kind() == slog.KindString {
		attr.Value = slog.StringValue(Redact(attr.Value.String()))
	}
	return attr
}

func MultiWriter(file io.Writer, fallback io.Writer) io.Writer {
	if fallback == nil {
		return file
	}
	return io.MultiWriter(fallback, file)
}
