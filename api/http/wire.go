// Package http adapts API services to bounded HTTP/JSON requests.
package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/lengzhao/GoChainKit/api"
	"github.com/lengzhao/GoChainKit/core"
	"net/http"
	"net/url"
)

func Hex(id core.ID) string { return hex.EncodeToString([]byte(id)) }
func ParseID(s string, maxBytes int) (core.ID, error) {
	if len(s) == 0 || len(s) > maxBytes*2 {
		return "", api.ErrInvalidInput
	}
	b, e := hex.DecodeString(s)
	if e != nil {
		return "", api.ErrInvalidInput
	}
	return core.ID(b), nil
}
func At(r *http.Request, maxBytes int) (core.ID, error) {
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return "", api.ErrInvalidInput
	}
	values, ok := query["at"]
	if !ok {
		return "", nil
	}
	if len(values) != 1 {
		return "", api.ErrInvalidInput
	}
	return ParseID(values[0], maxBytes)
}
func WriteJSON(w http.ResponseWriter, status int, v any) {
	data, e := json.Marshal(v)
	if e != nil {
		WriteError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
func errorClass(e error) (int, string) {
	if _, ok := e.(*api.InputError); ok {
		return 400, "invalid_input"
	}
	if many, ok := e.(interface{ Unwrap() []error }); ok {
		status, code := 0, ""
		for _, child := range many.Unwrap() {
			s, c := errorClass(child)
			if s == 500 || status != 0 && s != status {
				return 500, "internal"
			}
			status, code = s, c
		}
		if status != 0 {
			return status, code
		}
	}
	if one, ok := e.(interface{ Unwrap() error }); ok {
		return errorClass(one.Unwrap())
	}
	switch e {
	case api.ErrInvalidInput:
		return 400, "invalid_input"
	case api.ErrTooLarge:
		return 413, "too_large"
	case core.ErrInvalidTransaction:
		return 422, "invalid_transaction"
	case core.ErrNotFound:
		return 404, "not_found"
	case core.ErrClosed:
		return 503, "closed"
	case core.ErrFull:
		return 429, "capacity"
	case context.Canceled:
		return 408, "canceled"
	case context.DeadlineExceeded:
		return 504, "timeout"
	}
	return 500, "internal"
}
func WriteError(w http.ResponseWriter, e error) {
	status, code := errorClass(e)
	writeError(w, status, code)
}
func writeError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": http.StatusText(status)}})
}
