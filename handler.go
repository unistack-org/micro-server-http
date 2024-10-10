package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.unistack.org/micro/v3/errors"
	"go.unistack.org/micro/v3/logger"
	"go.unistack.org/micro/v3/metadata"
	"go.unistack.org/micro/v3/meter"
	"go.unistack.org/micro/v3/options"
	"go.unistack.org/micro/v3/register"
	"go.unistack.org/micro/v3/semconv"
	"go.unistack.org/micro/v3/server"
	"go.unistack.org/micro/v3/tracer"
	rhttp "go.unistack.org/micro/v3/util/http"
	rflutil "go.unistack.org/micro/v3/util/reflect"
)

var (
	DefaultErrorHandler = func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int) {
		w.WriteHeader(status)
		if _, cerr := w.Write([]byte(err.Error())); cerr != nil {
			logger.DefaultLogger.Errorf(ctx, "write failed: %v", cerr)
		}
	}
	DefaultContentType = "application/json"
)

type patHandler struct {
	mtype *methodType
	rcvr  reflect.Value
	name  string
}

type httpHandler struct {
	opts     server.HandlerOptions
	hd       interface{}
	handlers *rhttp.Trie
	name     string
	eps      []*register.Endpoint
	sopts    server.Options
	sync.RWMutex
}

func (h *httpHandler) Name() string {
	return h.name
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

func (h *Server) HTTPHandlerFunc(handler interface{}) (http.HandlerFunc, error) {
	if handler == nil {
		return nil, fmt.Errorf("invalid handler specified: %v", handler)
	}

	rtype := reflect.TypeOf(handler)
	if rtype.NumIn() != 3 {
		return nil, fmt.Errorf("invalid handler, NumIn != 3: %v", rtype.NumIn())
	}

	argType := rtype.In(1)
	replyType := rtype.In(2)

	// First arg need not be a pointer.
	if !isExportedOrBuiltinType(argType) {
		return nil, fmt.Errorf("invalid handler, argument type not exported: %v", argType)
	}

	if replyType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("invalid handler, reply type not a pointer: %v", replyType)
	}

	// Reply type must be exported.
	if !isExportedOrBuiltinType(replyType) {
		return nil, fmt.Errorf("invalid handler, reply type not exported: %v", replyType)
	}

	if rtype.NumOut() != 1 {
		return nil, fmt.Errorf("invalid handler, has wrong number of outs: %v", rtype.NumOut())
	}

	// The return type of the method must be error.
	if returnType := rtype.Out(0); returnType != typeOfError {
		return nil, fmt.Errorf("invalid handler, returns %v not error", returnType.String())
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ct := DefaultContentType
		if htype := r.Header.Get(metadata.HeaderContentType); htype != "" {
			ct = htype
		}

		ctx := context.WithValue(r.Context(), rspCodeKey{}, &rspCodeVal{})
		ctx = context.WithValue(ctx, rspHeaderKey{}, &rspHeaderVal{})
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(len(r.Header) + 8)
		}
		for k, v := range r.Header {
			md[k] = strings.Join(v, ", ")
		}
		md["RemoteAddr"] = r.RemoteAddr
		md["Method"] = r.Method
		md["URL"] = r.URL.String()
		md["Proto"] = r.Proto
		md["Content-Length"] = fmt.Sprintf("%d", r.ContentLength)
		md["Transfer-Encoding"] = strings.Join(r.TransferEncoding, ",")
		md["Host"] = r.Host
		md["RequestURI"] = r.RequestURI
		if r.TLS != nil {
			md["TLS"] = "true"
			md["TLS-ALPN"] = r.TLS.NegotiatedProtocol
			md["TLS-ServerName"] = r.TLS.ServerName
		}

		ctx = metadata.NewIncomingContext(ctx, md)

		path := r.URL.Path

		if r.Body != nil {
			defer r.Body.Close()
		}

		matches := make(map[string]interface{})
		var match bool
		var hldr *patHandler
		var handler *httpHandler

		for _, shdlr := range h.handlers {
			hdlr := shdlr.(*httpHandler)
			fh, mp, err := hdlr.handlers.Search(r.Method, path)
			if err == nil {
				match = true
				for k, v := range mp {
					matches[k] = v
				}
				hldr = fh.(*patHandler)
				handler = hdlr
				break
			} else if err == rhttp.ErrMethodNotAllowed && !h.registerRPC {
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte("not matching route found"))
				return
			}
		}

		if !match && h.registerRPC {
			microMethod, mok := md.Get(metadata.HeaderEndpoint)
			if mok {
				serviceMethod := strings.Split(microMethod, ".")
				if len(serviceMethod) == 2 {
					if shdlr, ok := h.handlers[serviceMethod[0]]; ok {
						hdlr := shdlr.(*httpHandler)
						fh, mp, err := hdlr.handlers.Search(http.MethodPost, "/"+microMethod)
						if err == nil {
							match = true
							for k, v := range mp {
								matches[k] = v
							}
							hldr = fh.(*patHandler)
							handler = hdlr
						}
					}
				}
			}
		}

		// get fields from url values
		if len(r.URL.RawQuery) > 0 {
			umd, cerr := rflutil.URLMap(r.URL.RawQuery)
			if cerr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(cerr.Error()))
				return
			}
			for k, v := range umd {
				matches[k] = v
			}
		}

		cf, err := h.newCodec(ct)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		var argv, replyv reflect.Value

		// Decode the argument value.
		argIsValue := false // if true, need to indirect before calling.
		if hldr.mtype.ArgType.Kind() == reflect.Ptr {
			argv = reflect.New(hldr.mtype.ArgType.Elem())
		} else {
			argv = reflect.New(hldr.mtype.ArgType)
			argIsValue = true
		}

		if argIsValue {
			argv = argv.Elem()
		}

		// reply value
		replyv = reflect.New(hldr.mtype.ReplyType.Elem())

		function := hldr.mtype.method.Func
		var returnValues []reflect.Value

		if r.Body != nil {
			var buf []byte
			buf, err = io.ReadAll(r.Body)
			if err != nil && err != io.EOF {
				h.errorHandler(ctx, handler, w, r, err, http.StatusInternalServerError)
				return
			}

			if err = cf.Unmarshal(buf, argv.Interface()); err != nil {
				h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
				return
			}
		}

		matches = rflutil.FlattenMap(matches)
		if err = rflutil.Merge(argv.Interface(), matches, rflutil.SliceAppend(true), rflutil.Tags([]string{"protobuf", "json"})); err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
			return
		}

		hr := &rpcRequest{
			codec:       cf,
			service:     handler.sopts.Name,
			contentType: ct,
			method:      fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name),
			endpoint:    fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name),
			payload:     argv.Interface(),
			header:      md,
		}

		// define the handler func
		fn := func(fctx context.Context, req server.Request, rsp interface{}) (err error) {
			returnValues = function.Call([]reflect.Value{hldr.rcvr, hldr.mtype.prepareContext(fctx), argv, reflect.ValueOf(rsp)})

			// The return value for the method is an error.
			if rerr := returnValues[0].Interface(); rerr != nil {
				err = rerr.(error)
			}

			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				md = metadata.New(0)
			}
			if nmd, ok := metadata.FromOutgoingContext(fctx); ok {
				for k, v := range nmd {
					md.Set(k, v)
				}
			}
			metadata.SetOutgoingContext(ctx, md)

			return err
		}

		// wrap the handler func
		h.opts.Hooks.EachNext(func(hook options.Hook) {
			if h, ok := hook.(server.HookHandler); ok {
				fn = h(fn)
			}
		})

		if ct == "application/x-www-form-urlencoded" {
			cf, err = h.newCodec(DefaultContentType)
			if err != nil {
				h.errorHandler(ctx, handler, w, r, err, http.StatusInternalServerError)
				return
			}
			ct = DefaultContentType
		}

		scode := int(200)
		appErr := fn(ctx, hr, replyv.Interface())

		w.Header().Set(metadata.HeaderContentType, ct)
		if md, ok := metadata.FromOutgoingContext(ctx); ok {
			for k, v := range md {
				w.Header().Set(k, v)
			}
		}
		if md := getRspHeader(ctx); md != nil {
			for k, v := range md {
				for _, vv := range v {
					w.Header().Add(k, vv)
				}
			}
		}
		if nct := w.Header().Get(metadata.HeaderContentType); nct != ct {
			if cf, err = h.newCodec(nct); err != nil {
				h.errorHandler(ctx, nil, w, r, err, http.StatusBadRequest)
				return
			}
		}

		var buf []byte
		if appErr != nil {
			switch verr := appErr.(type) {
			case *errors.Error:
				scode = int(verr.Code)
				buf, err = cf.Marshal(verr)
			case *Error:
				buf, err = cf.Marshal(verr.err)
			default:
				buf, err = cf.Marshal(appErr)
			}
		} else {
			buf, err = cf.Marshal(replyv.Interface())
		}

		if err != nil && handler.sopts.Logger.V(logger.ErrorLevel) {
			handler.sopts.Logger.Errorf(handler.sopts.Context, "handler err: %v", err)
			return
		}

		if nscode := GetRspCode(ctx); nscode != 0 {
			scode = nscode
		}
		w.WriteHeader(scode)

		if _, cerr := w.Write(buf); cerr != nil {
			handler.sopts.Logger.Errorf(ctx, "write failed: %v", cerr)
		}
	}, nil
}

func (h *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ct := DefaultContentType
	if htype := r.Header.Get(metadata.HeaderContentType); htype != "" {
		ct = htype
	}

	ts := time.Now()

	ctx := context.WithValue(r.Context(), rspCodeKey{}, &rspCodeVal{})
	ctx = context.WithValue(ctx, rspHeaderKey{}, &rspHeaderVal{})

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(len(r.Header) + 8)
	}
	for k, v := range r.Header {
		md[k] = strings.Join(v, ", ")
	}
	md["RemoteAddr"] = r.RemoteAddr
	if r.TLS != nil {
		md["Scheme"] = "https"
	} else {
		md["Scheme"] = "http"
	}
	md["Method"] = r.Method
	md["URL"] = r.URL.String()
	md["Proto"] = r.Proto
	md["ContentLength"] = fmt.Sprintf("%d", r.ContentLength)
	if len(r.TransferEncoding) > 0 {
		md["TransferEncoding"] = strings.Join(r.TransferEncoding, ",")
	}
	md["Host"] = r.Host
	md["RequestURI"] = r.RequestURI
	ctx = metadata.NewIncomingContext(ctx, md)

	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		h.errorHandler(ctx, nil, w, r, fmt.Errorf("path must starts with /"), http.StatusBadRequest)
		return
	}

	matches := make(map[string]interface{})

	var match bool
	var hldr *patHandler
	var handler *httpHandler

	for _, shdlr := range h.handlers {
		hdlr := shdlr.(*httpHandler)
		fh, mp, err := hdlr.handlers.Search(r.Method, path)
		if err == nil {
			match = true
			for k, v := range mp {
				matches[k] = v
			}
			hldr = fh.(*patHandler)
			handler = hdlr
			break
		} else if err == rhttp.ErrMethodNotAllowed && !h.registerRPC {
			h.errorHandler(ctx, nil, w, r, fmt.Errorf("not matching route found"), http.StatusMethodNotAllowed)
			return
		}
	}

	if !match && h.registerRPC {
		microMethod, mok := md.Get(metadata.HeaderEndpoint)
		if mok {
			serviceMethod := strings.Split(microMethod, ".")
			if len(serviceMethod) == 2 {
				if shdlr, ok := h.handlers[serviceMethod[0]]; ok {
					hdlr := shdlr.(*httpHandler)
					fh, mp, err := hdlr.handlers.Search(http.MethodPost, "/"+microMethod)
					if err == nil {
						match = true
						for k, v := range mp {
							matches[k] = v
						}
						hldr = fh.(*patHandler)
						handler = hdlr
					}
				}
			}
		}
	}

	var sp tracer.Span
	if !match && h.hd != nil {
		if hdlr, ok := h.hd.Handler().(http.Handler); ok {
			if !slices.Contains(tracer.DefaultSkipEndpoints, h.hd.Name()) {
				ctx, sp = h.opts.Tracer.Start(ctx, h.hd.Name()+" rpc-server",
					tracer.WithSpanKind(tracer.SpanKindServer),
					tracer.WithSpanLabels(
						"endpoint", h.hd.Name(),
					),
				)
				defer func() {
					n := GetRspCode(ctx)
					if s, _ := sp.Status(); s != tracer.SpanStatusError && n > 399 {
						sp.SetStatus(tracer.SpanStatusError, http.StatusText(n))
					}
					sp.Finish()
				}()
			}

			if !slices.Contains(meter.DefaultSkipEndpoints, h.hd.Name()) {
				h.opts.Meter.Counter(semconv.ServerRequestInflight, "endpoint", h.hd.Name()).Inc()

				defer func() {
					n := GetRspCode(ctx)
					if n > 399 {
						h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", h.hd.Name(), "status", "success", "code", strconv.Itoa(n)).Inc()
					} else {
						h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", h.hd.Name(), "status", "failure", "code", strconv.Itoa(n)).Inc()
					}
					te := time.Since(ts)
					h.opts.Meter.Summary(semconv.ServerRequestLatencyMicroseconds, "endpoint", h.hd.Name()).Update(te.Seconds())
					h.opts.Meter.Histogram(semconv.ServerRequestDurationSeconds, "endpoint", h.hd.Name()).Update(te.Seconds())
					h.opts.Meter.Counter(semconv.ServerRequestInflight, "endpoint", h.hd.Name()).Dec()
				}()
			}

			hdlr.ServeHTTP(w, r.WithContext(ctx))
			return
		}
	} else if !match {
		// check for http.HandlerFunc handlers
		if !slices.Contains(tracer.DefaultSkipEndpoints, r.URL.Path) {
			ctx, sp = h.opts.Tracer.Start(ctx, r.URL.Path+" rpc-server",
				tracer.WithSpanKind(tracer.SpanKindServer),
				tracer.WithSpanLabels(
					"endpoint", r.URL.Path,
				),
			)

			defer func() {
				if n := GetRspCode(ctx); n > 399 {
					sp.SetStatus(tracer.SpanStatusError, http.StatusText(n))
				} else {
					sp.SetStatus(tracer.SpanStatusError, http.StatusText(http.StatusNotFound))
				}
				sp.Finish()
			}()
		}
		if ph, _, err := h.pathHandlers.Search(r.Method, r.URL.Path); err == nil {
			ph.(http.HandlerFunc)(w, r.WithContext(ctx))
			return
		}
		h.errorHandler(ctx, nil, w, r, fmt.Errorf("not matching route found"), http.StatusNotFound)
		return
	}

	endpointName := fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name)
	topts := []tracer.SpanOption{
		tracer.WithSpanKind(tracer.SpanKindServer),
		tracer.WithSpanLabels(
			"endpoint", endpointName,
		),
	}

	if slices.Contains(tracer.DefaultSkipEndpoints, endpointName) {
		topts = append(topts, tracer.WithSpanRecord(false))
	}

	ctx, sp = h.opts.Tracer.Start(ctx, endpointName+" rpc-server", topts...)

	if !slices.Contains(meter.DefaultSkipEndpoints, handler.name) {
		defer func() {
			te := time.Since(ts)
			h.opts.Meter.Summary(semconv.ServerRequestLatencyMicroseconds, "endpoint", handler.name).Update(te.Seconds())
			h.opts.Meter.Histogram(semconv.ServerRequestDurationSeconds, "endpoint", handler.name).Update(te.Seconds())
			h.opts.Meter.Counter(semconv.ServerRequestInflight, "endpoint", handler.name).Dec()

			n := GetRspCode(ctx)
			if n > 399 {
				h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", handler.name, "status", "failure", "code", strconv.Itoa(n)).Inc()
			} else {
				h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", handler.name, "status", "success", "code", strconv.Itoa(n)).Inc()
			}
		}()
	}

	defer func() {
		n := GetRspCode(ctx)
		if n > 399 {
			if s, _ := sp.Status(); s != tracer.SpanStatusError {
				sp.SetStatus(tracer.SpanStatusError, http.StatusText(n))
			}
		}
		sp.Finish()
	}()

	// get fields from url values
	if len(r.URL.RawQuery) > 0 {
		umd, cerr := rflutil.URLMap(r.URL.RawQuery)
		if cerr != nil {
			h.errorHandler(ctx, handler, w, r, cerr, http.StatusBadRequest)
			return
		}
		for k, v := range umd {
			matches[k] = v
		}
	}

	if r.Body != nil {
		defer r.Body.Close()
	}

	cf, err := h.newCodec(ct)
	if err != nil {
		h.errorHandler(ctx, nil, w, r, err, http.StatusBadRequest)
		return
	}

	var argv, replyv reflect.Value

	// Decode the argument value.
	argIsValue := false // if true, need to indirect before calling.
	if hldr.mtype.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(hldr.mtype.ArgType.Elem())
	} else {
		argv = reflect.New(hldr.mtype.ArgType)
		argIsValue = true
	}

	if argIsValue {
		argv = argv.Elem()
	}

	// reply value
	replyv = reflect.New(hldr.mtype.ReplyType.Elem())

	function := hldr.mtype.method.Func
	var returnValues []reflect.Value

	if r.Body != nil {
		var buf []byte
		buf, err = io.ReadAll(r.Body)
		if err != nil && err != io.EOF {
			h.errorHandler(ctx, handler, w, r, err, http.StatusInternalServerError)
			return
		}

		if err = cf.Unmarshal(buf, argv.Interface()); err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
			return
		}
	}

	if len(matches) > 0 {
		matches = rflutil.FlattenMap(matches)
		if err = rflutil.Merge(argv.Interface(), matches, rflutil.SliceAppend(true), rflutil.Tags([]string{"protobuf", "json"})); err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
			return
		}
	}

	hr := &rpcRequest{
		codec:       cf,
		service:     handler.sopts.Name,
		contentType: ct,
		method:      fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name),
		endpoint:    fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name),
		payload:     argv.Interface(),
		header:      md,
	}

	// define the handler func
	fn := func(fctx context.Context, req server.Request, rsp interface{}) (err error) {
		returnValues = function.Call([]reflect.Value{hldr.rcvr, hldr.mtype.prepareContext(fctx), argv, reflect.ValueOf(rsp)})

		// The return value for the method is an error.
		if rerr := returnValues[0].Interface(); rerr != nil {
			err = rerr.(error)
		}

		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(0)
		}
		if nmd, ok := metadata.FromOutgoingContext(fctx); ok {
			for k, v := range nmd {
				md.Set(k, v)
			}
		}
		metadata.SetOutgoingContext(ctx, md)

		if err != nil && sp != nil {
			sp.SetStatus(tracer.SpanStatusError, err.Error())
		}

		return err
	}

	h.opts.Hooks.EachNext(func(hook options.Hook) {
		if h, ok := hook.(server.HookHandler); ok {
			fn = h(fn)
		}
	})

	if ct == "application/x-www-form-urlencoded" {
		cf, err = h.newCodec(DefaultContentType)
		if err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusInternalServerError)
			return
		}
		ct = DefaultContentType
	}

	scode := int(200)
	appErr := fn(ctx, hr, replyv.Interface())

	w.Header().Set(metadata.HeaderContentType, ct)
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		for k, v := range md {
			w.Header().Set(k, v)
		}
	}
	if md := getRspHeader(ctx); md != nil {
		for k, v := range md {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
	}
	if nct := w.Header().Get(metadata.HeaderContentType); nct != ct {
		if cf, err = h.newCodec(nct); err != nil {
			h.errorHandler(ctx, nil, w, r, err, http.StatusBadRequest)
			return
		}
	}

	var buf []byte
	if appErr != nil {
		switch verr := appErr.(type) {
		case *errors.Error:
			scode = int(verr.Code)
			buf, err = cf.Marshal(verr)
		case *Error:
			buf, err = cf.Marshal(verr.err)
		default:
			buf, err = cf.Marshal(appErr)
		}
	} else {
		buf, err = cf.Marshal(replyv.Interface())
	}

	if err != nil {
		if handler.sopts.Logger.V(logger.ErrorLevel) {
			handler.sopts.Logger.Error(handler.sopts.Context, "handler error", err)
		}
		scode = http.StatusInternalServerError
	} else if nscode := GetRspCode(ctx); nscode != 0 {
		scode = nscode
	}

	w.WriteHeader(scode)

	if _, cerr := w.Write(buf); cerr != nil {
		handler.sopts.Logger.Error(ctx, "respoonse write error", cerr)
	}
}
