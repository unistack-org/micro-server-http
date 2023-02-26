package meter // import "go.unistack.org/micro-server-http/v3/handler/meter"

import (
	"bytes"
	"context"

	codecpb "go.unistack.org/micro-proto/v3/codec"
	"go.unistack.org/micro/v3/errors"
	"go.unistack.org/micro/v3/meter"
)

// guard to fail early
var _ MeterServiceServer = &Handler{}

type Handler struct {
	opts Options
}

type Option func(*Options)

type Options struct {
	Meter        meter.Meter
	Name         string
	MeterOptions []meter.Option
}

func Meter(m meter.Meter) Option {
	return func(o *Options) {
		o.Meter = m
	}
}

func Name(name string) Option {
	return func(o *Options) {
		o.Name = name
	}
}

func MeterOptions(opts ...meter.Option) Option {
	return func(o *Options) {
		o.MeterOptions = append(o.MeterOptions, opts...)
	}
}

func NewOptions(opts ...Option) Options {
	options := Options{Meter: meter.DefaultMeter}
	for _, o := range opts {
		o(&options)
	}
	return options
}

func NewHandler(opts ...Option) *Handler {
	options := NewOptions(opts...)
	return &Handler{opts: options}
}

func (h *Handler) Metrics(ctx context.Context, req *codecpb.Frame, rsp *codecpb.Frame) error {
	buf := bytes.NewBuffer(nil)
	if err := h.opts.Meter.Write(buf, h.opts.MeterOptions...); err != nil {
		return errors.InternalServerError(h.opts.Name, "%v", err)
	}

	rsp.Data = buf.Bytes()

	return nil
}
