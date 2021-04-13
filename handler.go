package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/unistack-org/micro/v3/codec"
	"github.com/unistack-org/micro/v3/errors"
	"github.com/unistack-org/micro/v3/logger"
	"github.com/unistack-org/micro/v3/metadata"
	"github.com/unistack-org/micro/v3/register"
	"github.com/unistack-org/micro/v3/server"
	rflutil "github.com/unistack-org/micro/v3/util/reflect"
	rutil "github.com/unistack-org/micro/v3/util/router"
)

var (
	DefaultErrorHandler = func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int) {
		w.WriteHeader(status)
		w.Write([]byte(err.Error()))
	}
	DefaultContentType = "application/json"
)

type patHandler struct {
	pat   rutil.Pattern
	mtype *methodType
	name  string
	rcvr  reflect.Value
}

type httpHandler struct {
	name     string
	opts     server.HandlerOptions
	sopts    server.Options
	eps      []*register.Endpoint
	hd       interface{}
	handlers map[string][]patHandler
}

func (h *httpHandler) newCodec(ct string) (codec.Codec, error) {
	if cf, ok := h.sopts.Codecs[ct]; ok {
		return cf, nil
	}
	return nil, codec.ErrUnknownContentType
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

func (h *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	for exp, ph := range h.pathHandlers {
		if exp.MatchString(r.URL.String()) {
			ph(w, r)
			return
		}
	}

	ct := DefaultContentType
	if htype := r.Header.Get("Content-Type"); htype != "" {
		ct = htype
	}

	if ph, ok := h.contentTypeHandlers[strings.Split(ct, ";")[0]]; ok {
		ph(w, r)
		return
	}

	ctx := metadata.NewContext(r.Context(), nil)

	defer r.Body.Close()

	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		h.errorHandler(ctx, nil, w, r, fmt.Errorf("path must contains /"), http.StatusBadRequest)
		return
	}

	var cf codec.Codec
	var err error
	switch ct {
	case "application/x-www-form-urlencoded":
		cf, err = h.newCodec(strings.Split(DefaultContentType, ";")[0])
	default:
		cf, err = h.newCodec(strings.Split(ct, ";")[0])
	}

	if err != nil {
		h.errorHandler(ctx, nil, w, r, err, http.StatusBadRequest)
		return
	}

	components := strings.Split(path[1:], "/")
	l := len(components)
	var verb string
	idx := strings.LastIndex(components[l-1], ":")
	if idx == 0 {
		h.errorHandler(ctx, nil, w, r, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	if idx > 0 {
		c := components[l-1]
		components[l-1], verb = c[:idx], c[idx+1:]
	}

	matches := make(map[string]interface{})

	var match bool
	var hldr patHandler
	var handler *httpHandler
	for _, hpat := range h.handlers {
		handlertmp := hpat.(*httpHandler)
		for _, hldrtmp := range handlertmp.handlers[r.Method] {
			mp, err := hldrtmp.pat.Match(components, verb)
			if err == nil {
				match = true
				for k, v := range mp {
					matches[k] = v
				}
				hldr = hldrtmp
				handler = handlertmp
				break
			}
		}
	}

	if !match {
		h.errorHandler(ctx, nil, w, r, fmt.Errorf("not matching route found"), http.StatusNotFound)
		return
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(0)
	}

	for k, v := range r.Header {
		md.Set(k, strings.Join(v, ", "))
	}

	// get fields from url values
	if len(r.URL.RawQuery) > 0 {
		umd, err := rflutil.URLMap(r.URL.RawQuery)
		if err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
			return
		}
		for k, v := range umd {
			matches[k] = v
		}
	}

	if ct == "application/x-www-form-urlencoded" {
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			h.errorHandler(ctx, nil, w, r, err, http.StatusBadRequest)
			return
		}
		umd, err := rflutil.URLMap(string(buf))
		if err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
			return
		}
		for k, v := range umd {
			matches[k] = v
		}
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
	//function := hldr.rcvr
	var returnValues []reflect.Value

	if ct != "application/x-www-form-urlencoded" {
		if err = cf.ReadBody(r.Body, argv.Interface()); err != nil && err != io.EOF {
			h.errorHandler(ctx, handler, w, r, err, http.StatusInternalServerError)
			return
		}
	}

	matches = rflutil.FlattenMap(matches)
	if err = rflutil.Merge(argv.Interface(), matches, rflutil.SliceAppend(true), rflutil.Tags([]string{"protobuf", "json"})); err != nil {
		h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
		return
	}

	var b []byte
	if ct != "application/x-www-form-urlencoded" {
		b, err = cf.Marshal(argv.Interface())
		if err != nil {
			h.errorHandler(ctx, handler, w, r, err, http.StatusBadRequest)
			return
		}
	}

	hr := &rpcRequest{
		codec:       cf,
		service:     handler.sopts.Name,
		contentType: ct,
		method:      fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name),
		body:        b,
		payload:     argv.Interface(),
	}

	var scode int
	// define the handler func
	fn := func(fctx context.Context, req server.Request, rsp interface{}) (err error) {
		fctx = context.WithValue(fctx, rspCodeKey{}, &rspCodeVal{})
		fctx = metadata.NewIncomingContext(fctx, md)
		returnValues = function.Call([]reflect.Value{hldr.rcvr, hldr.mtype.prepareContext(fctx), reflect.ValueOf(argv.Interface()), reflect.ValueOf(rsp)})

		scode = GetRspCode(fctx)
		// The return value for the method is an error.
		if rerr := returnValues[0].Interface(); rerr != nil {
			err = rerr.(error)
		}

		if md, ok := metadata.FromOutgoingContext(fctx); ok {
			metadata.SetOutgoingContext(ctx, md)
		}

		return err
	}

	// wrap the handler func
	for i := len(handler.sopts.HdlrWrappers); i > 0; i-- {
		fn = handler.sopts.HdlrWrappers[i-1](fn)
	}

	if appErr := fn(ctx, hr, replyv.Interface()); appErr != nil {
		switch verr := appErr.(type) {
		case *errors.Error:
			scode = int(verr.Code)
			b, err = cf.Marshal(verr)
		case *Error:
			b, err = cf.Marshal(verr.err)
		default:
			b, err = cf.Marshal(appErr)
		}
	} else {
		b, err = cf.Marshal(replyv.Interface())
	}
	if err != nil && handler.sopts.Logger.V(logger.ErrorLevel) {
		handler.sopts.Logger.Errorf(handler.sopts.Context, "handler err: %v", err)
		return
	}

	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		for k, v := range md {
			w.Header().Set(k, v)
		}
	}

	w.Header().Set("content-Type", ct)
	if scode != 0 {
		w.WriteHeader(scode)
	} else {
		//handler.sopts.Logger.Warn(handler.sopts.Context, "response code not set in handler via SetRspCode(ctx, http.StatusXXX)")
		w.WriteHeader(200)
	}
	w.Write(b)
}
