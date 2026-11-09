package testutil

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
)

type HandlerTransport struct {
	Handler     http.Handler
	unreachable atomic.Bool
}

func (t *HandlerTransport) Unreachable() { t.unreachable.Store(true) }

func (t *HandlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.unreachable.Load() {
		return nil, errors.New("dial tcp: connection refused")
	}
	recorder := httptest.NewRecorder()
	t.Handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}

func (t *HandlerTransport) Client() *http.Client { return &http.Client{Transport: t} }
