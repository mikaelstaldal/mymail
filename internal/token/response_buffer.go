package token

import (
	"bytes"
	"io"
	"net/http"
	"os"
)

const responseMemoryLimit = 1 << 20

// responseBuffer keeps a scoped read's database snapshot open only while the
// API handler builds its response. Large raw messages spill to a private file.
type responseBuffer struct {
	header http.Header
	status int
	memory bytes.Buffer
	file   *os.File
	err    error
}

func newResponseBuffer() *responseBuffer {
	return &responseBuffer{header: make(http.Header)}
}

func (b *responseBuffer) Header() http.Header { return b.header }

func (b *responseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *responseBuffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.status == 0 {
		b.status = http.StatusOK
	}
	if b.file == nil && b.memory.Len()+len(p) > responseMemoryLimit {
		f, err := os.CreateTemp("", "mymail-token-response-*")
		if err != nil {
			b.err = err
			return 0, err
		}
		b.file = f
		if _, err := f.Write(b.memory.Bytes()); err != nil {
			b.err = err
			return 0, err
		}
		b.memory.Reset()
	}
	if b.file != nil {
		n, err := b.file.Write(p)
		b.err = err
		return n, err
	}
	return b.memory.Write(p)
}

func (b *responseBuffer) Close() {
	if b.file != nil {
		name := b.file.Name()
		_ = b.file.Close()
		_ = os.Remove(name)
	}
}

func (b *responseBuffer) CopyTo(w http.ResponseWriter) error {
	if b.err != nil {
		return b.err
	}
	var reader io.Reader = &b.memory
	if b.file != nil {
		if _, err := b.file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		reader = b.file
	}
	for key, values := range b.header {
		w.Header()[key] = values
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, err := io.Copy(w, reader)
	return err
}
