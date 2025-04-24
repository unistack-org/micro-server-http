package http

import (
	"context"
	"net/http"
)

type (
	rspStatusCodeKey struct{}
	rspStatusCodeVal struct {
		code int
	}
)

// SetResponseStatusCode sets the status code in the context.
func SetResponseStatusCode(ctx context.Context, code int) {
	if rsp, ok := ctx.Value(rspStatusCodeKey{}).(*rspStatusCodeVal); ok {
		rsp.code = code
	}
}

// GetResponseStatusCode retrieves the response status code from the context.
func GetResponseStatusCode(ctx context.Context) int {
	code := http.StatusOK
	if rsp, ok := ctx.Value(rspStatusCodeKey{}).(*rspStatusCodeVal); ok {
		code = rsp.code
	}
	return code
}
