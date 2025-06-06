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
	"time"

	"go.unistack.org/micro/v4/errors"
	"go.unistack.org/micro/v4/logger"
	"go.unistack.org/micro/v4/metadata"
	"go.unistack.org/micro/v4/meter"
	"go.unistack.org/micro/v4/options"
	"go.unistack.org/micro/v4/semconv"
	"go.unistack.org/micro/v4/server"
	"go.unistack.org/micro/v4/tracer"
	rhttp "go.unistack.org/micro/v4/util/http"
	rflutil "go.unistack.org/micro/v4/util/reflect"
)

var (
	DefaultErrorHandler = func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int) {
		w.WriteHeader(status)
		if _, cerr := w.Write([]byte(err.Error())); cerr != nil {
			logger.DefaultLogger.Error(ctx, "write error", cerr)
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
	sopts    server.Options
}

func (h *httpHandler) Name() string {
	return h.name
}

func (h *httpHandler) Handler() interface{} {
	return h.hd
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

		ctx := context.WithValue(r.Context(), rspStatusCodeKey{}, &rspStatusCodeVal{})
		ctx = context.WithValue(ctx, rspMetadataKey{}, &rspMetadataVal{m: metadata.New(0)})

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(len(r.Header) + 8)
		}
		for k, v := range r.Header {
			md[k] = append(md[k], v...)
		}

		md["RemoteAddr"] = append(md["RemoteAddr"], r.RemoteAddr)
		md["Method"] = append(md["Method"], r.Method)
		md["URL"] = append(md["URL"], r.URL.String())
		md["Proto"] = append(md["Proto"], r.Proto)
		md["Content-Length"] = append(md["Content-Length"], fmt.Sprintf("%d", r.ContentLength))
		md["Transfer-Encoding"] = append(md["Transfer-Encoding"], r.TransferEncoding...)
		md["Host"] = append(md["Host"], r.Host)
		md["RequestURI"] = append(md["RequestURI"], r.RequestURI)
		if r.TLS != nil {
			md["TLS"] = append(md["TLS"], "true")
			md["TLS-ALPN"] = append(md["TLS-ALPN"], r.TLS.NegotiatedProtocol)
			md["TLS-ServerName"] = append(md["TLS-ServerName"], r.TLS.ServerName)
		}

		ctx = metadata.NewIncomingContext(ctx, md)
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(0))

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
			for _, microMethod := range md.Get(metadata.HeaderEndpoint) {
				serviceMethod := strings.Split(microMethod, ".")
				if len(serviceMethod) == 2 {
					if shdlr, ok := h.handlers[serviceMethod[0]]; ok {
						hdlr := shdlr.(*httpHandler)
						fh, mp, err := hdlr.handlers.Search(http.MethodPost, "/"+microMethod)
						if err == nil {
							// match = true
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
			w.WriteHeader(http.StatusUnsupportedMediaType)
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

			return err
		}

		// wrap the handler func
		h.opts.Hooks.EachPrev(func(hook options.Hook) {
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
		for k, v := range getResponseMetadata(ctx) {
			w.Header()[k] = v
		}
		if nct := w.Header().Get(metadata.HeaderContentType); nct != ct {
			if cf, err = h.newCodec(nct); err != nil {
				h.errorHandler(ctx, nil, w, r, err, http.StatusInternalServerError)
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
			handler.sopts.Logger.Error(handler.sopts.Context, "handler error", err)
			return
		}

		if nscode := GetResponseStatusCode(ctx); nscode != 0 {
			scode = nscode
		}
		w.WriteHeader(scode)

		if _, cerr := w.Write(buf); cerr != nil {
			handler.sopts.Logger.Error(ctx, "write failed", cerr)
		}
	}, nil
}

func (h *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ct := DefaultContentType
	if htype := r.Header.Get(metadata.HeaderContentType); htype != "" {
		ct = htype
	}

	ts := time.Now()

	ctx := context.WithValue(r.Context(), rspStatusCodeKey{}, &rspStatusCodeVal{})
	ctx = context.WithValue(ctx, rspMetadataKey{}, &rspMetadataVal{m: metadata.New(0)})

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(len(r.Header) + 8)
	}
	for k, v := range r.Header {
		md[k] = append(md[k], v...)
	}

	md["RemoteAddr"] = append(md["RemoteAddr"], r.RemoteAddr)
	if r.TLS != nil {
		md["Scheme"] = append(md["Scheme"], "https")
	} else {
		md["Scheme"] = append(md["Scheme"], "http")
	}
	md["Method"] = append(md["Method"], r.Method)
	md["URL"] = append(md["URL"], r.URL.String())
	md["Proto"] = append(md["Proto"], r.Proto)
	md["Content-Length"] = append(md["Content-Length"], fmt.Sprintf("%d", r.ContentLength))
	if len(r.TransferEncoding) > 0 {
		md["Transfer-Encoding"] = append(md["Transfer-Encoding"], r.TransferEncoding...)
	}
	md["Host"] = append(md["Host"], r.Host)
	md["RequestURI"] = append(md["RequestURI"], r.RequestURI)

	ctx = metadata.NewIncomingContext(ctx, md)
	ctx = metadata.NewOutgoingContext(ctx, metadata.New(0))

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
		for _, microMethod := range md.Get(metadata.HeaderEndpoint) {
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
			endpointName := fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name)
			if !slices.Contains(tracer.DefaultSkipEndpoints, endpointName) {
				ctx, sp = h.opts.Tracer.Start(ctx, "rpc-server",
					tracer.WithSpanKind(tracer.SpanKindServer),
					tracer.WithSpanLabels(
						"endpoint", endpointName,
					),
				)
				defer func() {
					n := GetResponseStatusCode(ctx)
					if s, _ := sp.Status(); s != tracer.SpanStatusError && n > 399 {
						sp.SetStatus(tracer.SpanStatusError, http.StatusText(n))
					}
					sp.Finish()
				}()
			}

			if !slices.Contains(meter.DefaultSkipEndpoints, endpointName) {
				h.opts.Meter.Counter(semconv.ServerRequestInflight, "endpoint", endpointName, "server", "http").Inc()

				defer func() {
					n := GetResponseStatusCode(ctx)
					if n > 399 {
						h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", endpointName, "server", "http", "status", "success", "code", strconv.Itoa(n)).Inc()
					} else {
						h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", endpointName, "server", "http", "status", "failure", "code", strconv.Itoa(n)).Inc()
					}
					te := time.Since(ts)
					h.opts.Meter.Summary(semconv.ServerRequestLatencyMicroseconds, "endpoint", endpointName, "server", "http").Update(te.Seconds())
					h.opts.Meter.Histogram(semconv.ServerRequestDurationSeconds, "endpoint", endpointName, "server", "http").Update(te.Seconds())
					h.opts.Meter.Counter(semconv.ServerRequestInflight, "endpoint", endpointName, "server", "http").Dec()
				}()
			}

			hdlr.ServeHTTP(w, r.WithContext(ctx))
			return
		}
	} else if !match {
		// check for http.HandlerFunc handlers
		if !slices.Contains(tracer.DefaultSkipEndpoints, r.URL.Path) {
			ctx, sp = h.opts.Tracer.Start(ctx, "rpc-server",
				tracer.WithSpanKind(tracer.SpanKindServer),
				tracer.WithSpanLabels(
					"endpoint", r.URL.Path,
					"server", "http",
				),
			)

			defer func() {
				if n := GetResponseStatusCode(ctx); n > 399 {
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
			"server", "http",
		),
	}

	if slices.Contains(tracer.DefaultSkipEndpoints, endpointName) {
		topts = append(topts, tracer.WithSpanRecord(false))
	}

	ctx, sp = h.opts.Tracer.Start(ctx, "rpc-server", topts...)

	if !slices.Contains(meter.DefaultSkipEndpoints, handler.name) {
		defer func() {
			te := time.Since(ts)
			h.opts.Meter.Summary(semconv.ServerRequestLatencyMicroseconds, "endpoint", handler.name, "server", "http").Update(te.Seconds())
			h.opts.Meter.Histogram(semconv.ServerRequestDurationSeconds, "endpoint", handler.name, "server", "http").Update(te.Seconds())
			h.opts.Meter.Counter(semconv.ServerRequestInflight, "endpoint", handler.name, "server", "http").Dec()

			n := GetResponseStatusCode(ctx)
			if n > 399 {
				h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", handler.name, "server", "http", "status", "failure", "code", strconv.Itoa(n)).Inc()
			} else {
				h.opts.Meter.Counter(semconv.ServerRequestTotal, "endpoint", handler.name, "server", "http", "status", "success", "code", strconv.Itoa(n)).Inc()
			}
		}()
	}

	defer func() {
		n := GetResponseStatusCode(ctx)
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
		r.Body.Close()
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

		if err != nil && sp != nil {
			sp.SetStatus(tracer.SpanStatusError, err.Error())
		}

		return err
	}

	h.opts.Hooks.EachPrev(func(hook options.Hook) {
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
	for k, v := range getResponseMetadata(ctx) {
		w.Header()[k] = v
	}
	if nct := w.Header().Get(metadata.HeaderContentType); nct != ct {
		if cf, err = h.newCodec(nct); err != nil {
			h.errorHandler(ctx, nil, w, r, err, http.StatusInternalServerError)
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
	} else if nscode := GetResponseStatusCode(ctx); nscode != 0 {
		scode = nscode
	}

	w.WriteHeader(scode)

	if _, cerr := w.Write(buf); cerr != nil {
		handler.sopts.Logger.Error(ctx, "respoonse write error", cerr)
	}
}
