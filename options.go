package http

import (
	"context"
	"fmt"
	"net/http"

	"go.unistack.org/micro/v4/options"
)

// SetError pass error to caller
func SetError(err interface{}) error {
	return &Error{err: err}
}

// GetError return underline error
func GetError(err interface{}) interface{} {
	if verr, ok := err.(*Error); ok {
		return verr.err
	}
	return err
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

type (
	rspHeaderKey struct{}
	rspHeaderVal struct {
		h http.Header
	}
)

// SetRspHeader add response headers
func SetRspHeader(ctx context.Context, h http.Header) {
	if rsp, ok := ctx.Value(rspHeaderKey{}).(*rspHeaderVal); ok {
		rsp.h = h
	}
}

// SetRspCode saves response code in context, must be used by handler to specify http code
func SetRspCode(ctx context.Context, code int) {
	if rsp, ok := ctx.Value(rspCodeKey{}).(*rspCodeVal); ok {
		rsp.code = code
	}
}

// getRspHeader get http.Header from context
func getRspHeader(ctx context.Context) http.Header {
	if rsp, ok := ctx.Value(rspHeaderKey{}).(*rspHeaderVal); ok {
		return rsp.h
	}
	return nil
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
func Middleware(mw ...func(http.Handler) http.Handler) options.Option {
	return options.ContextOption(middlewareKey{}, mw)
}

type serverKey struct{}

// HTTPServer provide ability to pass *http.Server
func HTTPServer(hs *http.Server) options.Option {
	return options.ContextOption(serverKey{}, hs)
}

type errorHandler func(ctx context.Context, s interface{}, w http.ResponseWriter, r *http.Request, err error, status int)

type errorHandlerKey struct{}

// ErrorHandler specifies handler for errors
func ErrorHandler(fn errorHandler) options.Option {
	return options.ContextOption(errorHandlerKey{}, fn)
}

type (
	pathHandlerKey struct{}
	pathHandlerVal struct {
		h map[string]map[string]http.HandlerFunc
	}
)

// PathHandler specifies http handler for path regexp
func PathHandler(method, path string, handler http.HandlerFunc) options.Option {
	return func(src interface{}) error {
		vctx, err := options.Get(src, ".Context")
		if err != nil {
			return err
		}
		ctx, ok := vctx.(context.Context)
		if !ok {
			return fmt.Errorf("invalid option")
		}

		v, ok := ctx.Value(pathHandlerKey{}).(*pathHandlerVal)
		if !ok {
			v = &pathHandlerVal{h: make(map[string]map[string]http.HandlerFunc)}
		}
		m, ok := v.h[method]
		if !ok {
			m = make(map[string]http.HandlerFunc)
			v.h[method] = m
		}
		ctx = context.WithValue(ctx, pathHandlerKey{}, v)
		m[path] = handler
		return options.Set(src, ctx, ".Context")
	}
}

type registerRPCHandlerKey struct{}

// RegisterRPCHandler registers compatibility endpoints with /ServiceName.ServiceEndpoint method POST
func RegisterRPCHandler(b bool) options.Option {
	return options.ContextOption(registerRPCHandlerKey{}, b)
}

type handlerEndpointsKey struct{}

type EndpointMetadata struct {
	Name   string
	Path   string
	Method string
	Body   string
	Stream bool
}

func HandlerEndpoints(md []EndpointMetadata) options.Option {
	return options.ContextOption(handlerEndpointsKey{}, md)
}

type handlerOptions struct {
	headers []string
	cookies []string
}

type FillRequestOption func(*handlerOptions)

func Header(headers ...string) FillRequestOption {
	return func(o *handlerOptions) {
		o.headers = append(o.headers, headers...)
	}
}

func Cookie(cookies ...string) FillRequestOption {
	return func(o *handlerOptions) {
		o.cookies = append(o.cookies, cookies...)
	}
}
