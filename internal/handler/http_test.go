package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/flip1688/livescore/internal/service"
)

// writeError maps known sentinel errors to specific status codes and falls
// back to 500 for everything else. No catalog access involved, so this is
// exercised directly against a Handler with a nil catalog.
func TestWriteErrorStatusCodes(t *testing.T) {
	h := &Handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"bad input", service.ErrBadInput, http.StatusBadRequest},
		{"not found", service.ErrNotFound, http.StatusNotFound},
		{"other", errFake{}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/matches/1", nil)
			h.writeError(rr, req, tc.err)
			if rr.Code != tc.want {
				t.Errorf("writeError(%v) status = %d, want %d", tc.err, rr.Code, tc.want)
			}
		})
	}
}

type errFake struct{}

func (errFake) Error() string { return "fake error" }
