package meter // import "go.unistack.org/micro-server-http/v4/handler/meter"

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"
	"sync"

	codecpb "go.unistack.org/micro-proto/v4/codec"
	"go.unistack.org/micro/v4/errors"
	"go.unistack.org/micro/v4/logger"
	"go.unistack.org/micro/v4/metadata"
	"go.unistack.org/micro/v4/meter"
	"go.unistack.org/micro/v4/options"
)

const (
	contentEncodingHeader = "Content-Encoding"
	acceptEncodingHeader  = "Accept-Encoding"
)

var gzipPool = sync.Pool{
	New: func() interface{} {
		return gzip.NewWriter(nil)
	},
}

var bufPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(nil)
	},
}

// guard to fail early
var _ MeterServiceServer = (*Handler)(nil)

type Handler struct {
	opts Options
}

type Option func(*Options)

type Options struct {
	Meter        meter.Meter
	Name         string
	MeterOptions []options.Option
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

func MeterOptions(opts ...options.Option) Option {
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
	log, ok := logger.FromContext(ctx)
	if !ok {
		log = logger.DefaultLogger()
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()

	w := io.Writer(buf)

	if md, ok := metadata.FromContext(ctx); gzipAccepted(md) && ok {
		md.Set(contentEncodingHeader, "gzip")
		gz := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(gz)

		gz.Reset(w)
		defer gz.Close()

		w = gz
	}

	if err := h.opts.Meter.Write(w, h.opts.MeterOptions...); err != nil {
		log.Error(ctx, errors.InternalServerError(h.opts.Name, "%v", err))
		return nil
	}

	rsp.Data = buf.Bytes()

	return nil
}

// gzipAccepted returns whether the client will accept gzip-encoded content.
func gzipAccepted(md metadata.Metadata) bool {
	a, ok := md.Get(acceptEncodingHeader)
	if !ok {
		return false
	}
	if strings.Contains(a, "gzip") {
		return true
	}
	return false
}
