package health_handler

import (
	"context"

	codecpb "go.unistack.org/micro-proto/v4/codec"
	"go.unistack.org/micro/v4/errors"
)

var _ HealthServiceServer = &Handler{}

type Handler struct {
	opts Options
}

type (
	CheckFunc func(context.Context) error
	Option    func(*Options)
)

type Stater interface {
	Live() bool
	Ready() bool
	Health() bool
}

type Options struct {
	Version      string
	Name         string
	Staters      []Stater
	LiveChecks   []CheckFunc
	ReadyChecks  []CheckFunc
	HealthChecks []CheckFunc
}

func Service(s ...Stater) Option {
	return func(o *Options) {
		o.Staters = append(o.Staters, s...)
	}
}

func LiveChecks(fns ...CheckFunc) Option {
	return func(o *Options) {
		o.LiveChecks = append(o.LiveChecks, fns...)
	}
}

func ReadyChecks(fns ...CheckFunc) Option {
	return func(o *Options) {
		o.ReadyChecks = append(o.ReadyChecks, fns...)
	}
}

func HealthChecks(fns ...CheckFunc) Option {
	return func(o *Options) {
		o.HealthChecks = append(o.HealthChecks, fns...)
	}
}

func Name(name string) Option {
	return func(o *Options) {
		o.Name = name
	}
}

func Version(version string) Option {
	return func(o *Options) {
		o.Version = version
	}
}

func NewHandler(opts ...Option) *Handler {
	options := Options{}
	for _, o := range opts {
		o(&options)
	}
	return &Handler{opts: options}
}

func (h *Handler) Healthy(ctx context.Context, req *codecpb.Frame, rsp *codecpb.Frame) error {
	var err error

	for _, s := range h.opts.Staters {
		if !s.Health() {
			return errors.ServiceUnavailable(h.opts.Name, "%v", err)
		}
	}

	for _, fn := range h.opts.HealthChecks {
		if err = fn(ctx); err != nil {
			return errors.ServiceUnavailable(h.opts.Name, "%v", err)
		}
	}

	return nil
}

func (h *Handler) Live(ctx context.Context, req *codecpb.Frame, rsp *codecpb.Frame) error {
	var err error

	for _, s := range h.opts.Staters {
		if !s.Live() {
			return errors.ServiceUnavailable(h.opts.Name, "%v", err)
		}
	}

	for _, fn := range h.opts.LiveChecks {
		if err = fn(ctx); err != nil {
			return errors.ServiceUnavailable(h.opts.Name, "%v", err)
		}
	}

	return nil
}

func (h *Handler) Ready(ctx context.Context, req *codecpb.Frame, rsp *codecpb.Frame) error {
	var err error

	for _, s := range h.opts.Staters {
		if !s.Ready() {
			return errors.ServiceUnavailable(h.opts.Name, "%v", err)
		}
	}

	for _, fn := range h.opts.ReadyChecks {
		if err = fn(ctx); err != nil {
			return errors.ServiceUnavailable(h.opts.Name, "%v", err)
		}
	}
	return nil
}

func (h *Handler) Version(ctx context.Context, req *codecpb.Frame, rsp *codecpb.Frame) error {
	rsp.Data = []byte(h.opts.Version)
	return nil
}
