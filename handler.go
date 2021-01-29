package http

import (
	"github.com/unistack-org/micro/v3/register"
	"github.com/unistack-org/micro/v3/server"
)

type httpHandler struct {
	opts server.HandlerOptions
	eps  []*register.Endpoint
	hd   interface{}
}

func (h *httpHandler) Name() string {
	return "handler"
}

func (h *httpHandler) Handler() interface{} {
	return h.hd
}

func (h *httpHandler) Endpoints() []*register.Endpoint {
	return h.eps
}

func (h *httpHandler) Options() server.HandlerOptions {
	return h.opts
}
