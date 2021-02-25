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

func SetError(err interface{}) error {
	return &Error{err: err}
}

type Error struct {
	err interface{}
}

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

func Middleware(mw ...func(http.Handler) http.Handler) server.Option {
	return server.SetOption(middlewareKey{}, mw)
}

type serverKey struct{}

func Server(hs *http.Server) server.Option {
	return server.SetOption(serverKey{}, hs)
}
