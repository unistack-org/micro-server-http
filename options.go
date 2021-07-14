package http

import (
	"context"
	"fmt"
	"net/http"

	"github.com/unistack-org/micro/v3/server"
)

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

type (
	rspCodeKey struct{}
	rspCodeVal struct {
		code int
	}
)

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

type (
	pathHandlerKey struct{}
	pathHandlerVal struct {
		h map[string]http.HandlerFunc
	}
)

// PathHandler specifies http handler for path regexp
func PathHandler(path string, h http.HandlerFunc) server.Option {
	return func(o *server.Options) {
		if o.Context == nil {
			o.Context = context.Background()
		}
		v, ok := o.Context.Value(pathHandlerKey{}).(*pathHandlerVal)
		if !ok {
			v = &pathHandlerVal{h: make(map[string]http.HandlerFunc)}
		}
		v.h[path] = h
		o.Context = context.WithValue(o.Context, pathHandlerKey{}, v)
	}
}

type (
	contentTypeHandlerKey struct{}
	contentTypeHandlerVal struct {
		h map[string]http.HandlerFunc
	}
)

// ContentTypeHandler specifies http handler for Content-Type
func ContentTypeHandler(ct string, h http.HandlerFunc) server.Option {
	return func(o *server.Options) {
		if o.Context == nil {
			o.Context = context.Background()
		}
		v, ok := o.Context.Value(contentTypeHandlerKey{}).(*contentTypeHandlerVal)
		if !ok {
			v = &contentTypeHandlerVal{h: make(map[string]http.HandlerFunc)}
		}
		v.h[ct] = h
		o.Context = context.WithValue(o.Context, contentTypeHandlerKey{}, v)
	}
}

type registerRPCHandlerKey struct{}

// RegisterRPCHandler registers compatibility endpoints with /ServiceName.ServiceEndpoint method POST
func RegisterRPCHandler(b bool) server.Option {
	return server.SetOption(registerRPCHandlerKey{}, b)
}
