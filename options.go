package http

import (
	"context"
	"fmt"
	"net/http"

	"github.com/unistack-org/micro/v3/server"
)

type rspCodeKey struct{}
type rspCodeVal struct {
	code int
}

// SetError pass error to caller
func SetError(err interface{}) error {
	return &Error{err: err}
}

// Error struct holds error
type Error struct {
	err interface{}
}

// Error func for error interface
func (err *Error) Error() string {
	return fmt.Sprintf("%v", err.err)
}

// SetRspCode saves response code in context, must be used by handler to specify http code
func SetRspCode(ctx context.Context, code int) {
	if rsp, ok := ctx.Value(rspCodeKey{}).(*rspCodeVal); ok {
		rsp.code = code
	}
}

// GetRspCode used internally by generated http server handler
func GetRspCode(ctx context.Context) int {
	var code int
	if rsp, ok := ctx.Value(rspCodeKey{}).(*rspCodeVal); ok {
		code = rsp.code
	}
	return code
}

type middlewareKey struct{}

// Middleware passes http middlewares
func Middleware(mw ...func(http.Handler) http.Handler) server.Option {
	return server.SetOption(middlewareKey{}, mw)
}

type serverKey struct{}

// Server provide ability to pass *http.Server
func Server(hs *http.Server) server.Option {
	return server.SetOption(serverKey{}, hs)
}

type errorHandlerKey struct{}

// ErrorHandler specifies handler for errors
func ErrorHandler(fn func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int)) server.Option {
	return server.SetOption(errorHandlerKey{}, fn)
}
