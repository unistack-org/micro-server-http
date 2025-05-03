package http

import (
	"context"
	"fmt"
	"net/http"

	"go.unistack.org/micro/v4/server"
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

type middlewareKey struct{}

// Middleware passes http middlewares
func Middleware(mw ...func(http.Handler) http.Handler) server.Option {
	return server.SetOption(middlewareKey{}, mw)
}

type serverKey struct{}

// HTTPServer provide ability to pass *http.Server
func HTTPServer(hs *http.Server) server.Option {
	return server.SetOption(serverKey{}, hs)
}

type errorHandler func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int)

type errorHandlerKey struct{}

// ErrorHandler specifies handler for errors
func ErrorHandler(fn errorHandler) server.Option {
	return server.SetOption(errorHandlerKey{}, fn)
}

type (
	pathHandlerKey struct{}
	pathHandlerVal struct {
		h map[string]map[string]http.HandlerFunc
	}
)

// PathHandler specifies http handler for path regexp
func PathHandler(method, path string, handler http.HandlerFunc) server.Option {
	return func(o *server.Options) {
		if o.Context == nil {
			o.Context = context.Background()
		}
		v, ok := o.Context.Value(pathHandlerKey{}).(*pathHandlerVal)
		if !ok {
			v = &pathHandlerVal{h: make(map[string]map[string]http.HandlerFunc)}
		}
		m, ok := v.h[method]
		if !ok {
			m = make(map[string]http.HandlerFunc)
			v.h[method] = m
		}
		m[path] = handler
		o.Context = context.WithValue(o.Context, pathHandlerKey{}, v)
	}
}

type registerRPCHandlerKey struct{}

// RegisterRPCHandler registers compatibility endpoints with /ServiceName.ServiceEndpoint method POST
func RegisterRPCHandler(b bool) server.Option {
	return server.SetOption(registerRPCHandlerKey{}, b)
}

type registerCORSHandlerKey struct{}

// RegisterCORSHandler registers cors endpoints with /ServiceName.ServiceEndpoint method POPTIONSOST
func RegisterCORSHandler(b bool) server.HandlerOption {
	return server.SetHandlerOption(registerCORSHandlerKey{}, b)
}

type handlerEndpointsKey struct{}

type EndpointMetadata struct {
	Name   string
	Path   string
	Method string
	Body   string
	Stream bool
}

func HandlerEndpoints(md []EndpointMetadata) server.HandlerOption {
	return server.SetHandlerOption(handlerEndpointsKey{}, md)
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
