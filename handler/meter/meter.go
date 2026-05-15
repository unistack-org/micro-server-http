package meter_handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	http "net/http"
	"strings"
	"sync"

	codecpb "go.unistack.org/micro-proto/v5/codec"
	httpsrv "go.unistack.org/micro-server-http/v5"
	"go.unistack.org/micro/v5/logger"
	"go.unistack.org/micro/v5/metadata"
	"go.unistack.org/micro/v5/meter"
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
var _ MeterServiceServer = &Handler{}

type Handler struct {
	Options Options
}

type Option func(*Options)

type Options struct {
	Meter           meter.Meter
	Name            string
	MeterOptions    []meter.Option
	DisableCompress bool
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

func DisableCompress(g bool) Option {
	return func(o *Options) {
		o.DisableCompress = g
	}
}

func MeterOptions(opts ...meter.Option) Option {
	return func(o *Options) {
		o.MeterOptions = append(o.MeterOptions, opts...)
	}
}

func NewOptions(opts ...Option) Options {
	options := Options{Meter: meter.DefaultMeter, DisableCompress: false}
	for _, o := range opts {
		o(&options)
	}
	return options
}

func NewHandler(opts ...Option) *Handler {
	options := NewOptions(opts...)
	return &Handler{Options: options}
}

func (h *Handler) HTTPHandlerFunc(w http.ResponseWriter, r *http.Request) {
	var wr io.Writer
	if v := r.Header.Get(acceptEncodingHeader); strings.Contains(v, "gzip") && !h.Options.DisableCompress {
		w.Header().Add(contentEncodingHeader, "gzip")

		gz := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(gz)

		gz.Reset(w)
		defer gz.Close()

		wr = gz
	} else {
		wr = w
	}

	if err := h.Options.Meter.Write(wr, h.Options.MeterOptions...); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// gz.Flush() must be called after writing metrics to ensure buffered data is written to the underlying writer.
	if gz, ok := wr.(*gzip.Writer); ok {
		gz.Flush()
	}
}

func (h *Handler) Metrics(ctx context.Context, req *codecpb.Frame, rsp *codecpb.Frame) error {
	log, ok := logger.FromContext(ctx)
	if !ok {
		log = logger.DefaultLogger
	}

	var err error

	if md, ok := metadata.FromIncomingContext(ctx); ok && gzipAccepted(md) && !h.Options.DisableCompress {
		err = h.writeMetricsGzip(ctx, rsp)
	} else {
		err = h.writeMetricsPlain(ctx, rsp)
	}

	if err != nil {
		log.Error(ctx, "http/meter write failed", err)
	}

	return nil
}

func (h *Handler) writeMetricsGzip(ctx context.Context, rsp *codecpb.Frame) error {
	httpsrv.AppendResponseMetadata(ctx, metadata.Pairs(contentEncodingHeader, "gzip"))

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()

	gz := gzipPool.Get().(*gzip.Writer)
	defer gzipPool.Put(gz)
	gz.Reset(buf)

	if err := h.Options.Meter.Write(gz, h.Options.MeterOptions...); err != nil {
		return fmt.Errorf("meter write: %w", err)
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}

	rsp.Data = buf.Bytes()

	return nil
}

func (h *Handler) writeMetricsPlain(_ context.Context, rsp *codecpb.Frame) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()

	if err := h.Options.Meter.Write(buf, h.Options.MeterOptions...); err != nil {
		return fmt.Errorf("meter write: %w", err)
	}

	rsp.Data = buf.Bytes()

	return nil
}

// gzipAccepted returns whether the client will accept gzip-encoded content.
func gzipAccepted(md metadata.Metadata) bool {
	a := md.GetJoined(acceptEncodingHeader)

	return strings.Contains(a, "gzip")
}
