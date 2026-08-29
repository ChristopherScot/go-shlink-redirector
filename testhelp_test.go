package main

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// withChiParam attaches a chi URL parameter to the request's context so
// handlers using chi.URLParam can be exercised without a full router.
func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
