// Package http implements a go-micro.Server
package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/unistack-org/micro/v3/broker"
	"github.com/unistack-org/micro/v3/codec"
	"github.com/unistack-org/micro/v3/logger"
	"github.com/unistack-org/micro/v3/register"
	"github.com/unistack-org/micro/v3/server"
	rutil "github.com/unistack-org/micro/v3/util/router"
	"golang.org/x/net/netutil"
)

type httpServer struct {
	sync.RWMutex
	opts        server.Options
	handlers    map[string]server.Handler
	hd          server.Handler
	exit        chan chan error
	subscribers map[*httpSubscriber][]broker.Subscriber
	// used for first registration
	registered bool
	// register service instance
	rsvc *register.Service

	errorHandler func(context.Context, server.Handler, http.ResponseWriter, *http.Request, error, int)
}

func (h *httpServer) newCodec(ct string) (codec.Codec, error) {
	if cf, ok := h.opts.Codecs[ct]; ok {
		return cf, nil
	}
	return nil, codec.ErrUnknownContentType
}

func (h *httpServer) Options() server.Options {
	h.Lock()
	opts := h.opts
	h.Unlock()
	return opts
}

func (h *httpServer) Init(opts ...server.Option) error {
	h.Lock()
	for _, o := range opts {
		o(&h.opts)
	}
	h.errorHandler = DefaultErrorHandler
	if fn, ok := h.opts.Context.Value(errorHandlerKey{}).(func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int)); ok && fn != nil {
		h.errorHandler = fn
	}

	h.handlers = make(map[string]server.Handler)
	h.Unlock()
	return nil
}

func (h *httpServer) Handle(handler server.Handler) error {
	h.Lock()
	if hdlr, ok := handler.(*httpHandler); ok {
		if h.handlers == nil {
			h.handlers = make(map[string]server.Handler)
		}
		if _, ok := hdlr.hd.(http.Handler); ok {
			h.hd = handler
		} else {
			h.handlers[handler.Name()] = handler
		}
	} else {
		h.hd = handler
	}
	h.Unlock()
	return nil
}

func (h *httpServer) NewHandler(handler interface{}, opts ...server.HandlerOption) server.Handler {
	options := server.NewHandlerOptions(opts...)

	var eps []*register.Endpoint

	if !options.Internal {
		for name, metadata := range options.Metadata {
			eps = append(eps, &register.Endpoint{
				Name:     name,
				Metadata: metadata,
			})
		}
	}

	hdlr := &httpHandler{
		eps:   eps,
		hd:    handler,
		opts:  options,
		sopts: h.opts,
	}

	tp := reflect.TypeOf(handler)

	/*
	   for m := 0; m < tp.NumMethod(); m++ {
	   	    if e := register.ExtractEndpoint(tp.Method(m)); e != nil {
	   	    	      e.Name = name + "." + e.Name

	   	    	            for k, v := range options.Metadata[e.Name] {
	   	    	            	        e.Metadata[k] = v
	   	    	            	              }

	   	    	            	                    eps = append(eps, e)
	   	    	            	                        }
	   	    	            	                          }

	*/

	hdlr.handlers = make(map[string][]patHandler)
	for hn, md := range options.Metadata {
		cmp, err := rutil.Parse(md["Path"])
		if err != nil && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Errorf(h.opts.Context, "parsing path pattern err: %v", err)
			continue
		}
		tpl := cmp.Compile()
		pat, err := rutil.NewPattern(tpl.Version, tpl.OpCodes, tpl.Pool, tpl.Verb)
		if err != nil && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Errorf(h.opts.Context, "creating new pattern err: %v", err)
			continue
		}

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
			h.opts.Logger.Errorf(h.opts.Context, "nil method for %s", mname)
			continue
		}

		mtype, err := prepareEndpoint(method)
		if err != nil && h.opts.Logger.V(logger.ErrorLevel) {
			h.opts.Logger.Errorf(h.opts.Context, "%v", err)
			continue
		} else if mtype == nil {
			continue
		}

		rcvr := reflect.ValueOf(handler)
		name := reflect.Indirect(rcvr).Type().Name()

		pth := patHandler{pat: pat, mtype: mtype, name: name, rcvr: rcvr}
		hdlr.name = name
		hdlr.handlers[md["Method"]] = append(hdlr.handlers[md["Method"]], pth)
	}

	return hdlr
}

func (h *httpServer) NewSubscriber(topic string, handler interface{}, opts ...server.SubscriberOption) server.Subscriber {
	return newSubscriber(topic, handler, opts...)
}

func (h *httpServer) Subscribe(sb server.Subscriber) error {
	sub, ok := sb.(*httpSubscriber)
	if !ok {
		return fmt.Errorf("invalid subscriber: expected *httpSubscriber")
	}
	if len(sub.handlers) == 0 {
		return fmt.Errorf("invalid subscriber: no handler functions")
	}

	if err := server.ValidateSubscriber(sb); err != nil {
		return err
	}

	h.Lock()
	defer h.Unlock()
	_, ok = h.subscribers[sub]
	if ok {
		return fmt.Errorf("subscriber %v already exists", h)
	}
	h.subscribers[sub] = nil
	return nil
}

func (h *httpServer) Register() error {
	var eps []*register.Endpoint
	h.RLock()
	for _, hdlr := range h.handlers {
		eps = append(eps, hdlr.Endpoints()...)
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
	service.Nodes[0].Metadata["protocol"] = "http"
	service.Endpoints = eps

	h.Lock()
	var subscriberList []*httpSubscriber
	for e := range h.subscribers {
		// Only advertise non internal subscribers
		if !e.Options().Internal {
			subscriberList = append(subscriberList, e)
		}
	}
	sort.Slice(subscriberList, func(i, j int) bool {
		return subscriberList[i].topic > subscriberList[j].topic
	})

	for _, e := range subscriberList {
		service.Endpoints = append(service.Endpoints, e.Endpoints()...)
	}
	h.Unlock()

	h.RLock()
	registered := h.registered
	h.RUnlock()

	if !registered {
		if config.Logger.V(logger.InfoLevel) {
			config.Logger.Infof(config.Context, "Register [%s] Registering node: %s", config.Register.String(), service.Nodes[0].Id)
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
	defer h.Unlock()

	for sb := range h.subscribers {
		handler := h.createSubHandler(sb, config)
		var opts []broker.SubscribeOption
		if queue := sb.Options().Queue; len(queue) > 0 {
			opts = append(opts, broker.SubscribeGroup(queue))
		}

		subCtx := config.Context
		if cx := sb.Options().Context; cx != nil {
			subCtx = cx
		}
		opts = append(opts, broker.SubscribeContext(subCtx))
		opts = append(opts, broker.SubscribeAutoAck(sb.Options().AutoAck))

		sub, err := config.Broker.Subscribe(subCtx, sb.Topic(), handler, opts...)
		if err != nil {
			return err
		}
		h.subscribers[sb] = []broker.Subscriber{sub}
	}

	h.registered = true
	h.rsvc = service

	return nil
}

func (h *httpServer) Deregister() error {
	h.RLock()
	config := h.opts
	h.RUnlock()

	service, err := server.NewRegisterService(h)
	if err != nil {
		return err
	}

	if config.Logger.V(logger.InfoLevel) {
		config.Logger.Infof(config.Context, "Deregistering node: %s", service.Nodes[0].Id)
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

	subCtx := h.opts.Context
	for sb, subs := range h.subscribers {
		if cx := sb.Options().Context; cx != nil {
			subCtx = cx
		}

		for _, sub := range subs {
			config.Logger.Infof(config.Context, "Unsubscribing from topic: %s", sub.Topic())
			if err := sub.Unsubscribe(subCtx); err != nil {
				config.Logger.Errorf(config.Context, "failed to unsubscribe topic: %s, error: %v", sb.Topic(), err)
				return err
			}
		}
		h.subscribers[sb] = nil
	}
	h.Unlock()
	return nil
}

func (h *httpServer) Start() error {
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
		config.Logger.Infof(config.Context, "Listening on %s", ts.Addr().String())
	}

	h.Lock()
	h.opts.Address = ts.Addr().String()
	h.Unlock()

	var handler http.Handler
	if h.hd == nil {
		handler = h
	} else if hdlr, ok := h.hd.Handler().(http.Handler); ok {
		handler = hdlr
	}

	//if !ok {
	//	return errors.New("Server required http.Handler")
	//}

	if err := config.Broker.Connect(h.opts.Context); err != nil {
		return err
	}

	if err := config.RegisterCheck(h.opts.Context); err != nil {
		if config.Logger.V(logger.ErrorLevel) {
			config.Logger.Errorf(config.Context, "Server %s-%s register check error: %s", config.Name, config.Id, err)
		}
	} else {
		if err = h.Register(); err != nil {
			return err
		}
	}

	fn := handler
	var srvFunc func(net.Listener) error

	if h.opts.Context != nil {
		if mwf, ok := h.opts.Context.Value(middlewareKey{}).([]func(http.Handler) http.Handler); ok && len(mwf) > 0 {
			// wrap the handler func
			for i := len(mwf); i > 0; i-- {
				fn = mwf[i-1](fn)
			}
		}
		if hs, ok := h.opts.Context.Value(serverKey{}).(*http.Server); ok && hs != nil {
			hs.Handler = fn
			srvFunc = hs.Serve
		}
	}

	if srvFunc != nil {
		go srvFunc(ts)
	} else {
		go http.Serve(ts, fn)
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
				if rerr != nil && registered {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Errorf(config.Context, "Server %s-%s register check error: %s, deregister it", config.Name, config.Id, rerr)
					}
					// deregister self in case of error
					if err := h.Deregister(); err != nil {
						if config.Logger.V(logger.ErrorLevel) {
							config.Logger.Errorf(config.Context, "Server %s-%s deregister error: %s", config.Name, config.Id, err)
						}
					}
				} else if rerr != nil && !registered {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Errorf(config.Context, "Server %s-%s register check error: %s", config.Name, config.Id, rerr)
					}
					continue
				}
				if err := h.Register(); err != nil {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Errorf(config.Context, "Server %s-%s register error: %s", config.Name, config.Id, err)
					}
				}

				if err := h.Register(); err != nil {
					config.Logger.Errorf(config.Context, "Server register error: %s", err)
				}
			// wait for exit
			case ch = <-h.exit:
				break Loop
			}
		}

		ch <- ts.Close()

		// deregister
		if err := h.Deregister(); err != nil {
			config.Logger.Errorf(config.Context, "Server deregister error: %s", err)
		}

		if err := config.Broker.Disconnect(config.Context); err != nil {
			config.Logger.Errorf(config.Context, "Broker disconnect error: %s", err)
		}
	}()

	return nil
}

func (h *httpServer) Stop() error {
	ch := make(chan error)
	h.exit <- ch
	return <-ch
}

func (h *httpServer) String() string {
	return "http"
}

func (h *httpServer) Name() string {
	return h.opts.Name
}

func NewServer(opts ...server.Option) server.Server {
	options := server.NewOptions(opts...)
	return &httpServer{
		opts:        options,
		exit:        make(chan chan error),
		subscribers: make(map[*httpSubscriber][]broker.Subscriber),
	}
}
