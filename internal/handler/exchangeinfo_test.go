package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteExchangeInfo(t *testing.T) {
	recorder := httptest.NewRecorder()
	data := []byte(`{"timezone":"UTC","symbols":[]}`)

	if err := writeExchangeInfo(recorder, data); err != nil {
		t.Fatalf("writeExchangeInfo() error = %v", err)
	}

	result := recorder.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	if got := result.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := result.Header.Get("Data-Source"); got != "cache" {
		t.Fatalf("Data-Source = %q, want cache", got)
	}
	if got := recorder.Body.String(); got != string(data) {
		t.Fatalf("body = %q, want %q", got, string(data))
	}
}

func TestWriteExchangeInfoReturnsWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	writer := errResponseWriter{
		header: http.Header{},
		err:    wantErr,
	}

	if err := writeExchangeInfo(writer, []byte(`{}`)); !errors.Is(err, wantErr) {
		t.Fatalf("writeExchangeInfo() error = %v, want %v", err, wantErr)
	}

	if got := writer.header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := writer.header.Get("Data-Source"); got != "cache" {
		t.Fatalf("Data-Source = %q, want cache", got)
	}
}

type errResponseWriter struct {
	header http.Header
	err    error
}

func (w errResponseWriter) Header() http.Header {
	return w.header
}

func (w errResponseWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (w errResponseWriter) WriteHeader(int) {}
