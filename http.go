// Package http implements a go-micro.Server
package http

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	jsonrpc "github.com/unistack-org/micro-codec-jsonrpc"
	protorpc "github.com/unistack-org/micro-codec-protorpc"
	"github.com/unistack-org/micro/v3/broker"
	"github.com/unistack-org/micro/v3/codec"
	"github.com/unistack-org/micro/v3/logger"
	"github.com/unistack-org/micro/v3/registry"
	"github.com/unistack-org/micro/v3/server"
)

var (
	defaultCodecs = map[string]codec.NewCodec{
		"application/json":         jsonrpc.NewCodec,
		"application/json-rpc":     jsonrpc.NewCodec,
		"application/protobuf":     protorpc.NewCodec,
		"application/proto-rpc":    protorpc.NewCodec,
		"application/octet-stream": protorpc.NewCodec,
	}
)

type httpServer struct {
	sync.RWMutex
	opts        server.Options
	hd          server.Handler
	exit        chan chan error
	subscribers map[*httpSubscriber][]broker.Subscriber
	// used for first registration
	registered bool
	// registry service instance
	rsvc *registry.Service
}

func (h *httpServer) newCodec(contentType string) (codec.NewCodec, error) {
	if cf, ok := h.opts.Codecs[contentType]; ok {
		return cf, nil
	}
	if cf, ok := defaultCodecs[contentType]; ok {
		return cf, nil
	}
	return nil, fmt.Errorf("Unsupported Content-Type: %s", contentType)
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
	h.Unlock()
	return nil
}

func (h *httpServer) Handle(handler server.Handler) error {
	if _, ok := handler.Handler().(http.Handler); !ok {
		return errors.New("Handle requires http.Handler")
	}
	h.Lock()
	h.hd = handler
	h.Unlock()
	return nil
}

func (h *httpServer) NewHandler(handler interface{}, opts ...server.HandlerOption) server.Handler {
	options := server.HandlerOptions{
		Metadata: make(map[string]map[string]string),
	}

	for _, o := range opts {
		o(&options)
	}

	var eps []*registry.Endpoint

	if !options.Internal {
		for name, metadata := range options.Metadata {
			eps = append(eps, &registry.Endpoint{
				Name:     name,
				Metadata: metadata,
			})
		}
	}

	return &httpHandler{
		eps:  eps,
		hd:   handler,
		opts: options,
	}
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
	h.RLock()
	eps := h.hd.Endpoints()
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

	service, err := server.NewRegistryService(h)
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
			config.Logger.Info("Registry [%s] Registering node: %s", config.Registry.String(), service.Nodes[0].Id)
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

	service, err := server.NewRegistryService(h)
	if err != nil {
		return err
	}

	if config.Logger.V(logger.InfoLevel) {
		config.Logger.Info("Deregistering node: %s", service.Nodes[0].Id)
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
			config.Logger.Info("Unsubscribing from topic: %s", sub.Topic())
			if err := sub.Unsubscribe(subCtx); err != nil {
				config.Logger.Error("failed to unsubscribe topic: %s, error: %v", sb.Topic(), err)
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
	hd := h.hd
	h.RUnlock()

	ln, err := net.Listen("tcp", config.Address)
	if err != nil {
		return err
	}

	if config.Logger.V(logger.InfoLevel) {
		config.Logger.Info("Listening on %s", ln.Addr().String())
	}

	h.Lock()
	h.opts.Address = ln.Addr().String()
	h.Unlock()

	handler, ok := hd.Handler().(http.Handler)
	if !ok {
		return errors.New("Server required http.Handler")
	}

	if err = config.Broker.Connect(h.opts.Context); err != nil {
		return err
	}

	if err = config.RegisterCheck(h.opts.Context); err != nil {
		if config.Logger.V(logger.ErrorLevel) {
			config.Logger.Error("Server %s-%s register check error: %s", config.Name, config.Id, err)
		}
	} else {
		if err = h.Register(); err != nil {
			return err
		}
	}

	go http.Serve(ln, handler)

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
						config.Logger.Error("Server %s-%s register check error: %s, deregister it", config.Name, config.Id, rerr)
					}
					// deregister self in case of error
					if err := h.Deregister(); err != nil {
						if config.Logger.V(logger.ErrorLevel) {
							config.Logger.Error("Server %s-%s deregister error: %s", config.Name, config.Id, err)
						}
					}
				} else if rerr != nil && !registered {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error("Server %s-%s register check error: %s", config.Name, config.Id, rerr)
					}
					continue
				}
				if err := h.Register(); err != nil {
					if config.Logger.V(logger.ErrorLevel) {
						config.Logger.Error("Server %s-%s register error: %s", config.Name, config.Id, err)
					}
				}

				if err := h.Register(); err != nil {
					config.Logger.Error("Server register error: ", err)
				}
			// wait for exit
			case ch = <-h.exit:
				break Loop
			}
		}

		ch <- ln.Close()

		// deregister
		h.Deregister()

		config.Broker.Disconnect(config.Context)
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

func NewServer(opts ...server.Option) server.Server {
	options := server.NewOptions(opts...)
	return &httpServer{
		opts:        options,
		exit:        make(chan chan error),
		subscribers: make(map[*httpSubscriber][]broker.Subscriber),
	}
}
