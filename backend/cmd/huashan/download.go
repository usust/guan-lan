package main

import (
	"net/http"
	"strings"
	"time"
)

// Downloads share bandwidth under load. Refresh the write deadline on each
// chunk so active transfers can outlive the server's absolute WriteTimeout.
// A client that stops reading still times out after two minutes.
func withDownloadTimeout(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/api/v1/attachments/") {
			w = &downloadWriter{ResponseWriter: w}
		}
		handler.ServeHTTP(w, r)
	})
}

type downloadWriter struct{ http.ResponseWriter }

func (w *downloadWriter) Write(p []byte) (int, error) {
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(2 * time.Minute)); err != nil && err != http.ErrNotSupported {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

func (w *downloadWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
