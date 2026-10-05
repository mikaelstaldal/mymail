package token

import "net/http"

// releaseReadWriter ends the authorization snapshot when response encoding
// begins. Scoped handlers finish all database reads before their encoders
// write. Raw, headers, and attachment readers hold byte slices; body readers
// hold a string. A future scoped route must materialize its response before
// writing, or a lazy database read will fail after the transaction closes.
// Do not expose Unwrap: it would let a controller bypass release on output.
type releaseReadWriter struct {
	http.ResponseWriter
	release func()
}

func (w releaseReadWriter) WriteHeader(status int) {
	w.release()
	w.ResponseWriter.WriteHeader(status)
}

func (w releaseReadWriter) Write(p []byte) (int, error) {
	w.release()
	return w.ResponseWriter.Write(p)
}
