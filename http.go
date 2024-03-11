// Package http implements a go-micro.Server
package http // import "go.unistack.org/micro-server-http/v4"

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"go.unistack.org/micro/v4/codec"
	"go.unistack.org/micro/v4/logger"
	"go.unistack.org/micro/v4/options"
	"go.unistack.org/micro/v4/register"
	"go.unistack.org/micro/v4/server"
	rhttp "go.unistack.org/micro/v4/util/http"
	"golang.org/x/net/netutil"
)

var _ server.Server = (*Server)(nil)

type Server struct {
	hd           interface{}
	rsvc         *register.Service
	handlers     map[string]interface{}
	exit         chan chan error
	errorHandler func(context.Context, interface{}, http.ResponseWriter, *http.Request, error, int)
	pathHandlers *rhttp.Trie
	opts         server.Options
	registerRPC  bool
	sync.RWMutex
	registered bool
	init       bool
}

func (h *Server) newCodec(ct string) (codec.Codec, error) {
	if idx := strings.IndexRune(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}
	h.RLock()
	cf, ok := h.opts.Codecs[ct]
	h.RUnlock()
	if ok {
		return cf, nil
	}
	return nil, codec.ErrUnknownContentType
}

func (h *Server) Options() server.Options {
	h.Lock()
	opts := h.opts
	h.Unlock()
	return opts
}

func (h *Server) Init(opts ...options.Option) error {
	if len(opts) == 0 && h.init {
		return nil
	}

	h.Lock()

	for _, o := range opts {
		o(&h.opts)
	}
	if fn, ok := h.opts.Context.Value(errorHandlerKey{}).(func(ctx context.Context, s interface{}, w http.ResponseWriter, r *http.Request, err error, status int)); ok && fn != nil {
		h.errorHandler = fn
	}
	if h.handlers == nil {
		h.handlers = make(map[string]interface{})
	}
	if h.pathHandlers == nil {
		h.pathHandlers = rhttp.NewTrie()
	}

	if v, ok := h.opts.Context.Value(registerRPCHandlerKey{}).(bool); ok {
		h.registerRPC = v
	}

	if phs, ok := h.opts.Context.Value(pathHandlerKey{}).(*pathHandlerVal); ok && phs.h != nil {
		for pm, ps := range phs.h {
			for pp, ph := range ps {
				if err := h.pathHandlers.Insert([]string{pm}, pp, ph); err != nil {
					h.Unlock()
					return err
				}
			}
		}
	}
	h.Unlock()

	h.RLock()
	if err := h.opts.Register.Init(); err != nil {
		h.RUnlock()
		return err
	}
	if err := h.opts.Tracer.Init(); err != nil {
		h.RUnlock()
		return err
	}
	if err := h.opts.Logger.Init(); err != nil {
		h.RUnlock()
		return err
	}
	if err := h.opts.Meter.Init(); err != nil {
		h.RUnlock()
		return err
	}
	h.RUnlock()

	h.Lock()
	h.init = true
	h.Unlock()

	return nil
}

func (h *Server) Handle(handler interface{}, opts ...options.Option) error {
	options := server.NewHandleOptions(opts...)
	var endpointMetadata []EndpointMetadata

	if v, ok := options.Context.Value(handlerEndpointsKey{}).([]EndpointMetadata); ok {
		endpointMetadata = v
	}

	// passed unknown handler
	hdlr, ok := handler.(*httpHandler)
	if !ok {
		h.Lock()
		if h.handlers == nil {
			h.handlers = make(map[string]interface{})
		}
		for _, v := range endpointMetadata {
			h.handlers[v.Name] = h.newHTTPHandler(handler, opts...)
		}
		h.Unlock()
		return nil
	}

	// passed http.Handler like some muxer
	if _, ok := hdlr.hd.(http.Handler); ok {
		h.Lock()
		h.hd = handler
		h.Unlock()
		return nil
	}

	return nil
}

func (h *Server) newHTTPHandler(handler interface{}, opts ...options.Option) *httpHandler {
	options := server.NewHandleOptions(opts...)

	eps := make([]*register.Endpoint, 0, len(options.Metadata))
	for name, metadata := range options.Metadata {
		eps = append(eps, &register.Endpoint{
			Name:     name,
			Metadata: metadata,
		})
	}

	hdlr := &httpHandler{
		eps:      eps,
		hd:       handler,
		opts:     options,
		sopts:    h.opts,
		handlers: rhttp.NewTrie(),
	}

	tp := reflect.TypeOf(handler)

	/*
		if len(options.Metadata) == 0 {
			if h.registerRPC {
				h.opts.Logger.Infof(h.opts.Context, "register rpc handler for http.MethodPost %s /%s", hn, hn)
				if err := hdlr.handlers.Insert([]string{http.MethodPost}, "/"+hn, pth); err != nil {
					h.opts.Logger.Errorf(h.opts.Context, "cant add rpc handler for http.MethodPost %s /%s", hn, hn)
				}
			}
		}
	*/

	for hn, md := range options.Metadata {
		var method reflect.Method
		mname := hn[strings.Index(hn, ".")+1:]
		for m := 0; m < tp.NumMethod(); m++ {
			mn := tp.Method(m)
			if mn.Name != mname {
				continue
			}
			method = mn
			break
		}

		if method.Name == "" && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("nil method for %s", mname))
			continue
		}

		mtype, err := prepareEndpoint(method)
		if err != nil && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("%v", err))
			continue
		} else if mtype == nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("nil mtype for %s", mname))
			continue
		}

		rcvr := reflect.ValueOf(handler)
		name := reflect.Indirect(rcvr).Type().Name()

		pth := &patHandler{mtype: mtype, name: name, rcvr: rcvr}
		hdlr.name = name

		if err := hdlr.handlers.Insert(md["Method"], md["Path"][0], pth); err != nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add handler for %s %s", md["Method"][0], md["Path"][0]))
		}

		if h.registerRPC {
			h.opts.Logger.Info(h.opts.Context, fmt.Sprintf("register rpc handler for http.MethodPost %s /%s", hn, hn))
			if err := hdlr.handlers.Insert([]string{http.MethodPost}, "/"+hn, pth); err != nil {
				h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add rpc handler for http.MethodPost %s /%s", hn, hn))
			}
		}
	}

	metadata, ok := options.Context.Value(handlerEndpointsKey{}).([]EndpointMetadata)
	if !ok {
		return hdlr
	}

	for _, md := range metadata {
		hn := md.Name
		var method reflect.Method
		mname := hn[strings.Index(hn, ".")+1:]
		for m := 0; m < tp.NumMethod(); m++ {
			mn := tp.Method(m)
			if mn.Name != mname {
				continue
			}
			method = mn
			break
		}

		if method.Name == "" && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("nil method for %s", mname))
			continue
		}

		mtype, err := prepareEndpoint(method)
		if err != nil && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("%v", err))
			continue
		} else if mtype == nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("nil mtype for %s", mname))
			continue
		}

		rcvr := reflect.ValueOf(handler)
		name := reflect.Indirect(rcvr).Type().Name()

		pth := &patHandler{mtype: mtype, name: name, rcvr: rcvr}
		hdlr.name = name

		if err := hdlr.handlers.Insert([]string{md.Method}, md.Path, pth); err != nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add handler for %s %s", md.Method, md.Path))
		}

		if h.registerRPC {
			h.opts.Logger.Info(h.opts.Context, fmt.Sprintf("register rpc handler for http.MethodPost %s /%s", hn, hn))
			if err := hdlr.handlers.Insert([]string{http.MethodPost}, "/"+hn, pth); err != nil {
				h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add rpc handler for http.MethodPost %s /%s", hn, hn))
			}
		}
	}

	return hdlr
}

func (h *Server) Register() error {
	var eps []*register.Endpoint
	h.RLock()
	for _, hdlr := range h.handlers {
		hd, ok := hdlr.(*httpHandler)
		if !ok {
			continue
		}
		eps = append(eps, hd.Endpoints()...)
	}
	rsvc := h.rsvc
	config := h.opts
	h.RUnlock()

	// if service already filled, reuse it and return early
	if rsvc != nil {
		if err := server.DefaultRegisterFunc(rsvc, config); err != nil {
			return err
		}
		return nil
	}

	service, err := server.NewRegisterService(h)
	if err != nil {
		return err
	}
	service.Nodes[0].Metadata.Set("protocol", "http")
	service.Endpoints = eps

	h.RLock()
	registered := h.registered
	h.RUnlock()

	if !registered {
		if config.Logger.V(logger.InfoLevel) {
			config.Logger.Info(config.Context, fmt.Sprintf("Register [%s] Registering node: %s", config.Register.String(), service.Nodes[0].ID))
		}
	}

	// register the service
	if err := server.DefaultRegisterFunc(service, config); err != nil {
		return err
	}

	// already registered? don't need to register subscribers
	if registered {
		return nil
	}

	h.Lock()
	h.registered = true
	h.rsvc = service
	h.Unlock()

	return nil
}

func (h *Server) Deregister() error {
	h.RLock()
	config := h.opts
	h.RUnlock()

	service, err := server.NewRegisterService(h)
	if err != nil {
		return err
	}

	if config.Logger.V(logger.InfoLevel) {
		config.Logger.Info(config.Context, fmt.Sprintf("Deregistering node: %s", service.Nodes[0].ID))
	}

	if err := server.DefaultDeregisterFunc(service, config); err != nil {
		return err
	}

	h.Lock()
	h.rsvc = nil

	if !h.registered {
		h.Unlock()
		return nil
	}

	h.registered = false
	h.Unlock()
	return nil
}

func (h *Server) Start() error {
	h.RLock()
	config := h.opts
	h.RUnlock()

	// micro: config.Transport.Listen(config.Address)
	var ts net.Listener

	if l := config.Listener; l != nil {
		ts = l
	} else {
		var err error

		// check the tls config for secure connect
		if tc := config.TLSConfig; tc != nil {
			ts, err = tls.Listen("tcp", config.Address, tc)
			// otherwise just plain tcp listener
		} else {
			ts, err = net.Listen("tcp", config.Address)
		}
		if err != nil {
			return err
		}
	}

	if config.MaxConn > 0 {
		ts = netutil.LimitListener(ts, config.MaxConn)
	}

	if config.Logger.V(logger.InfoLevel) {
		config.Logger.Info(config.Context, fmt.Sprintf("Listening on %s", ts.Addr().String()))
	}

	h.Lock()
	h.opts.Address = ts.Addr().String()
	h.Unlock()

	var handler http.Handler

	// nolint: nestif
	if h.opts.Context != nil {
		if hs, ok := h.opts.Context.Value(serverKey{}).(*http.Server); ok && hs != nil {
			if hs.Handler == nil && h.hd != nil {
				if hdlr, ok := h.hd.(http.Handler); ok {
					hs.Handler = hdlr
					handler = hs.Handler
				}
			} else {
				handler = hs.Handler
			}
		}
	}

	switch {
	case handler == nil && h.hd == nil:
		handler = h
	case len(h.handlers) > 0 && h.hd != nil:
		handler = h
	case handler == nil && h.hd != nil:
		if hdlr, ok := h.hd.(http.Handler); ok {
			handler = hdlr
		}
	}

	if handler == nil {
		return fmt.Errorf("cant process with nil handler")
	}

	if err := config.RegisterCheck(h.opts.Context); err != nil {
		if config.Logger.V(logger.ErrorLevel) {
			config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register check error: %s", config.Name, config.ID, err))
		}
	} else {
		if err = h.Register(); err != nil {
			return err
		}
	}

	fn := handler

	var hs *http.Server
	var srvFunc func(net.Listener) error
	if h.opts.Context != nil {
		if mwf, ok := h.opts.Context.Value(middlewareKey{}).([]func(http.Handler) http.Handler); ok && len(mwf) > 0 {
			// wrap the handler func
			for i := len(mwf); i > 0; i-- {
				fn = mwf[i-1](fn)
			}
		}
		var ok bool
		if hs, ok = h.opts.Context.Value(serverKey{}).(*http.Server); ok && hs != nil {
			hs.Handler = fn
			srvFunc = hs.Serve
		}
	}

	if srvFunc != nil {
		go func() {
			if cerr := srvFunc(ts); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
				h.opts.Logger.Error(h.opts.Context, "failed to serve", cerr)
			}
		}()
	} else {
		go func() {
			hs = &http.Server{Handler: fn}
			if cerr := hs.Serve(ts); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
				h.opts.Logger.Error(h.opts.Context, "failed to serve", cerr)
			}
		}()
	}

	go func() {
		t := new(time.Ticker)

		// only process if it exists
		if config.RegisterInterval > time.Duration(0) {
			// new ticker
			t = time.NewTicker(config.RegisterInterval)
		}

		// return error chan
		var ch chan error

	Loop:
		for {
			select {
			// register self on interval
			case <-t.C:
				h.RLock()
				registered := h.registered
				h.RUnlock()
				rerr := config.RegisterCheck(h.opts.Context)
				// nolint: nestif
				if rerr != nil && registered {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register check error: %s, deregister it", config.Name, config.ID, rerr))
					}
					// deregister self in case of error
					if err := h.Deregister(); err != nil {
						if config.Logger.V(logger.ErrorLevel) {
							config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s deregister error: %s", config.Name, config.ID, err))
						}
					}
				} else if rerr != nil && !registered {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register check error: %s", config.Name, config.ID, rerr))
					}
					continue
				}
				if err := h.Register(); err != nil {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register error: %s", config.Name, config.ID, err))
					}
				}

				if err := h.Register(); err != nil {
					config.Logger.Error(config.Context, fmt.Sprintf("Server register error: %s", err))
				}
			// wait for exit
			case ch = <-h.exit:
				break Loop
			}
		}

		// deregister
		if err := h.Deregister(); err != nil {
			config.Logger.Error(config.Context, fmt.Sprintf("Server deregister error: %s", err))
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.opts.GracefulTimeout)
		defer cancel()

		err := hs.Shutdown(ctx)
		if err != nil {
			err = hs.Close()
		}

		ch <- err
	}()

	return nil
}

func (h *Server) Stop() error {
	ch := make(chan error)
	h.exit <- ch
	return <-ch
}

func (h *Server) String() string {
	return "http"
}

func (h *Server) Name() string {
	return h.opts.Name
}

func NewServer(opts ...options.Option) *Server {
	options := server.NewOptions(opts...)
	eh := DefaultErrorHandler
	if v, ok := options.Context.Value(errorHandlerKey{}).(errorHandler); ok && v != nil {
		eh = v
	}
	return &Server{
		opts:         options,
		exit:         make(chan chan error),
		errorHandler: eh,
		pathHandlers: rhttp.NewTrie(),
	}
}
