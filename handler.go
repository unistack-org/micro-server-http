package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/unistack-org/micro/v3/codec"
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
	name         string
	opts         server.HandlerOptions
	sopts        server.Options
	eps          []*register.Endpoint
	hd           interface{}
	handlers     map[string][]patHandler
	errorHandler func(context.Context, server.Handler, http.ResponseWriter, *http.Request, error, int)
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

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	defer r.Body.Close()

	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		h.errorHandler(ctx, h, w, r, fmt.Errorf("path must contains /"), http.StatusBadRequest)
	}

	ct := DefaultContentType
	if htype := r.Header.Get("Content-Type"); htype != "" {
		ct = htype
	}

	cf, err := h.newCodec(ct)
	if err != nil {
		h.errorHandler(ctx, h, w, r, err, http.StatusBadRequest)
	}

	components := strings.Split(path[1:], "/")
	l := len(components)
	var verb string
	idx := strings.LastIndex(components[l-1], ":")
	if idx == 0 {
		h.errorHandler(ctx, h, w, r, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	if idx > 0 {
		c := components[l-1]
		components[l-1], verb = c[:idx], c[idx+1:]
	}

	matches := make(map[string]interface{})
	var match bool
	var hldr patHandler
	for _, hldr = range h.handlers[r.Method] {
		mp, err := hldr.pat.Match(components, verb)
		if err == nil {
			match = true
			for k, v := range mp {
				matches[k] = v
			}
			break
		}
	}

	if !match {
		h.errorHandler(ctx, h, w, r, fmt.Errorf("not matching route found"), http.StatusNotFound)
		return
	}

	md, ok := metadata.FromContext(ctx)
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
			h.errorHandler(ctx, h, w, r, err, http.StatusBadRequest)
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

	if err = cf.ReadBody(r.Body, argv.Interface()); err != nil && err != io.EOF {
		h.errorHandler(ctx, h, w, r, err, http.StatusInternalServerError)
	}

	matches = rflutil.FlattenMap(matches)
	if err = rflutil.MergeMap(argv.Interface(), matches); err != nil {
		h.errorHandler(ctx, h, w, r, err, http.StatusBadRequest)
		return
	}

	b, err := cf.Marshal(argv.Interface())
	if err != nil {
		h.errorHandler(ctx, h, w, r, err, http.StatusBadRequest)
		return
	}

	hr := &rpcRequest{
		codec:       cf,
		service:     h.sopts.Name,
		contentType: ct,
		method:      fmt.Sprintf("%s.%s", hldr.name, hldr.mtype.method.Name),
		body:        b,
		payload:     argv.Interface(),
	}

	var scode int
	// define the handler func
	fn := func(ctx context.Context, req server.Request, rsp interface{}) (err error) {
		ctx = context.WithValue(ctx, rspCodeKey{}, &rspCodeVal{})
		ctx = metadata.NewContext(ctx, md)
		returnValues = function.Call([]reflect.Value{hldr.rcvr, hldr.mtype.prepareContext(ctx), reflect.ValueOf(argv.Interface()), reflect.ValueOf(rsp)})

		scode = GetRspCode(ctx)
		// The return value for the method is an error.
		if rerr := returnValues[0].Interface(); rerr != nil {
			err = rerr.(error)
		}

		return err
	}

	// wrap the handler func
	for i := len(h.sopts.HdlrWrappers); i > 0; i-- {
		fn = h.sopts.HdlrWrappers[i-1](fn)
	}

	if appErr := fn(ctx, hr, replyv.Interface()); appErr != nil {
		b, err = cf.Marshal(appErr)
	} else {
		b, err = cf.Marshal(replyv.Interface())
	}
	if err != nil && h.sopts.Logger.V(logger.ErrorLevel) {
		h.sopts.Logger.Errorf(h.sopts.Context, "XXXXX: %v", err)
		return
	}

	w.Header().Set("content-Type", ct)
	if scode != 0 {
		w.WriteHeader(scode)
	} else {
		h.sopts.Logger.Warn(h.sopts.Context, "response code not set in handler via SetRspCode(ctx, http.StatusXXX)")
	}
	w.Write(b)
}
