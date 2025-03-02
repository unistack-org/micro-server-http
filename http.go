// Package http implements a go-micro.Server
package http // import "go.unistack.org/micro-server-http/v3"

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
	"sync/atomic"
	"time"

	"go.unistack.org/micro/v4/codec"
	"go.unistack.org/micro/v4/logger"
	"go.unistack.org/micro/v4/register"
	"go.unistack.org/micro/v4/server"
	rhttp "go.unistack.org/micro/v4/util/http"
	"golang.org/x/net/netutil"
)

var _ server.Server = (*Server)(nil)

type Server struct {
	hd           server.Handler
	rsvc         *register.Service
	handlers     map[string]server.Handler
	exit         chan chan error
	errorHandler func(context.Context, server.Handler, http.ResponseWriter, *http.Request, error, int)
	pathHandlers *rhttp.Trie
	opts         server.Options
	stateLive    *atomic.Uint32
	stateReady   *atomic.Uint32
	stateHealth  *atomic.Uint32
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

func (h *Server) Init(opts ...server.Option) error {
	if len(opts) == 0 && h.init {
		return nil
	}

	h.Lock()

	for _, o := range opts {
		o(&h.opts)
	}
	if fn, ok := h.opts.Context.Value(errorHandlerKey{}).(func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int)); ok && fn != nil {
		h.errorHandler = fn
	}
	if h.handlers == nil {
		h.handlers = make(map[string]server.Handler)
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
	if err := h.opts.Broker.Init(); err != nil {
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

func (h *Server) Handle(handler server.Handler) error {
	// passed unknown handler
	hdlr, ok := handler.(*httpHandler)
	if !ok {
		h.Lock()
		h.hd = handler
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

	// passed micro compat handler
	h.Lock()
	if h.handlers == nil {
		h.handlers = make(map[string]server.Handler)
	}
	h.handlers[handler.Name()] = handler
	h.Unlock()

	return nil
}

func (h *Server) NewHandler(handler interface{}, opts ...server.HandlerOption) server.Handler {
	options := server.NewHandlerOptions(opts...)

	hdlr := &httpHandler{
		hd:       handler,
		opts:     options,
		sopts:    h.opts,
		handlers: rhttp.NewTrie(),
	}

	tp := reflect.TypeOf(handler)

	registerCORS := false
	if v, ok := options.Context.Value(registerCORSHandlerKey{}).(bool); ok && v {
		registerCORS = true
	}

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
			h.opts.Logger.Error(h.opts.Context, "endpoint error", err)
			continue
		} else if mtype == nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("nil mtype for %s", mname))
			continue
		}

		rcvr := reflect.ValueOf(handler)
		name := reflect.Indirect(rcvr).Type().Name()

		pth := &patHandler{mtype: mtype, name: name, rcvr: rcvr}
		hdlr.name = name

		methods := md["Method"]
		if registerCORS {
			methods = append(methods, http.MethodOptions)
		}

		pattern := md["Path"]
		for i := len(pattern) - 1; i >= 0; i-- {
			if err := hdlr.handlers.Insert(methods, pattern[i], pth); err != nil {
				h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add handler for %v %s: index %d", methods, md["Path"], i))
			}
		}

		if h.registerRPC {
			methods := []string{http.MethodPost}
			if registerCORS {
				methods = append(methods, http.MethodOptions)
			}

			if err := hdlr.handlers.Insert(methods, "/"+hn, pth); err != nil {
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
			h.opts.Logger.Error(h.opts.Context, "prepare endpoint error", err)
			continue
		} else if mtype == nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("nil mtype for %s", mname))
			continue
		}

		rcvr := reflect.ValueOf(handler)
		name := reflect.Indirect(rcvr).Type().Name()

		pth := &patHandler{mtype: mtype, name: name, rcvr: rcvr}
		hdlr.name = name

		methods := []string{md.Method}
		if registerCORS {
			methods = append(methods, http.MethodOptions)
		}

		if err := hdlr.handlers.Insert(methods, md.Path, pth); err != nil {
			h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add handler for %s %s", md.Method, md.Path))
		}

		if h.registerRPC {
			methods := []string{http.MethodPost}
			if registerCORS {
				methods = append(methods, http.MethodOptions)
			}

			h.opts.Logger.Info(h.opts.Context, fmt.Sprintf("register rpc handler for http.MethodPost %s /%s", hn, hn))
			if err := hdlr.handlers.Insert(methods, "/"+hn, pth); err != nil {
				h.opts.Logger.Error(h.opts.Context, fmt.Sprintf("cant add rpc handler for http.MethodPost %s /%s", hn, hn))
			}
		}
	}

	return hdlr
}

func (h *Server) Register() error {
	h.RLock()
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
		config.Logger.Info(config.Context, "Deregistering node: "+service.Nodes[0].ID)
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
		config.Logger.Info(config.Context, "Listening on "+ts.Addr().String())
	}

	h.Lock()
	h.opts.Address = ts.Addr().String()
	h.Unlock()

	var handler http.Handler

	// nolint: nestif
	if h.opts.Context != nil {
		if hs, ok := h.opts.Context.Value(serverKey{}).(*http.Server); ok && hs != nil {
			if hs.Handler == nil && h.hd != nil {
				if hdlr, ok := h.hd.Handler().(http.Handler); ok {
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
		if hdlr, ok := h.hd.Handler().(http.Handler); ok {
			handler = hdlr
		}
	}

	if handler == nil {
		return fmt.Errorf("cant process with nil handler")
	}

	if err := config.Broker.Connect(h.opts.Context); err != nil {
		return err
	}

	if err := config.RegisterCheck(h.opts.Context); err != nil {
		if config.Logger.V(logger.ErrorLevel) {
			config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register check error", config.Name, config.ID), err)
		}
	} else {
		if err = h.Register(); err != nil {
			return err
		}
	}

	fn := handler

	var hs *http.Server
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
		} else {
			hs = &http.Server{Handler: fn}
		}
	}

	go func() {
		if cerr := hs.Serve(ts); cerr != nil && !errors.Is(cerr, http.ErrServerClosed) {
			h.opts.Logger.Error(h.opts.Context, "serve error", cerr)
		}
		h.stateLive.Store(0)
		h.stateReady.Store(0)
		h.stateHealth.Store(0)
	}()

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
						config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register check error, deregister it", config.Name, config.ID), rerr)
					}
					// deregister self in case of error
					if err := h.Deregister(); err != nil {
						if config.Logger.V(logger.ErrorLevel) {
							config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s deregister error", config.Name, config.ID), err)
						}
					}
				} else if rerr != nil && !registered {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register check error", config.Name, config.ID), rerr)
					}
					continue
				}
				if err := h.Register(); err != nil {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error(config.Context, fmt.Sprintf("Server %s-%s register error", config.Name, config.ID), err)
					}
				}

				if err := h.Register(); err != nil {
					config.Logger.Error(config.Context, "Server register error", err)
				}
			// wait for exit
			case ch = <-h.exit:
				break Loop
			}
		}

		// deregister
		if err := h.Deregister(); err != nil {
			config.Logger.Error(config.Context, "Server deregister error", err)
		}

		if err := config.Broker.Disconnect(config.Context); err != nil {
			config.Logger.Error(config.Context, "Broker disconnect error", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.opts.GracefulTimeout)
		defer cancel()

		err := hs.Shutdown(ctx)
		if err != nil {
			err = hs.Close()
		}

		ch <- err
	}()

	h.stateLive.Store(1)
	h.stateReady.Store(1)
	h.stateHealth.Store(1)

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

func (h *Server) Live() bool {
	return h.stateLive.Load() == 1
}

func (h *Server) Ready() bool {
	return h.stateReady.Load() == 1
}

func (h *Server) Health() bool {
	return h.stateHealth.Load() == 1
}

func NewServer(opts ...server.Option) *Server {
	options := server.NewOptions(opts...)
	eh := DefaultErrorHandler
	if v, ok := options.Context.Value(errorHandlerKey{}).(errorHandler); ok && v != nil {
		eh = v
	}
	return &Server{
		stateLive:    &atomic.Uint32{},
		stateReady:   &atomic.Uint32{},
		stateHealth:  &atomic.Uint32{},
		opts:         options,
		exit:         make(chan chan error),
		errorHandler: eh,
		pathHandlers: rhttp.NewTrie(),
	}
}
