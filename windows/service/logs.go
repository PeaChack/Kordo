//go:build windows

package service

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"sync"
)

type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func openRotating(path string, max int64) (*rotatingFile, error) {
	_ = rotateIfLarge(path, max)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	st, _ := f.Stat()
	return &rotatingFile{path: path, max: max, f: f, size: st.Size()}, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > r.max {
		r.f.Close()
		_ = os.Rename(r.path, r.path+".1")
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		r.f, r.size = f, 0
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func rotateIfLarge(path string, max int64) error {
	st, err := os.Stat(path)
	if err != nil || st.Size() < max {
		return nil
	}
	return os.Rename(path, path+".1")
}

func ioWriter(f *rotatingFile, console bool) io.Writer {
	if console {
		return io.MultiWriter(f, os.Stderr)
	}
	return f
}

// secretRe masks values that look like credentials in diagnostics output.
var secretRe = regexp.MustCompile(`(?i)("?(uuid|password|private_key|pre_shared_key|public_key|secret|short_id)"?\s*[:=]\s*"?)([^",\s}]+)`)

func maskSecrets(b []byte) []byte {
	return secretRe.ReplaceAll(b, []byte(`${1}***`))
}

// tail returns the last n lines of a file.
func tail(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, _ := f.Stat()
	const chunk = 256 << 10
	off := st.Size() - chunk
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return "", err
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // first line is partial
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return string(maskSecrets(bytes.Join(lines, []byte("\n")))), nil
}
