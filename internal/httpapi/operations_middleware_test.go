package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverPanicReturnsInternalError(t *testing.T) {
	t.Parallel()

	handler := RecoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("injected")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func TestLimitRequestBodyRejectsOversizeContentLength(t *testing.T) {
	t.Parallel()

	handler := LimitRequestBody(4)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("12345"))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}
