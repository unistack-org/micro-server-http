package http

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"

	"go.unistack.org/micro/v3/broker"
	"go.unistack.org/micro/v3/codec"
	"go.unistack.org/micro/v3/metadata"
	"go.unistack.org/micro/v3/register"
	"go.unistack.org/micro/v3/server"
)

var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

type handler struct {
	reqType reflect.Type
	ctxType reflect.Type
	method  reflect.Value
}

type httpSubscriber struct {
	topic      string
	rcvr       reflect.Value
	typ        reflect.Type
	subscriber interface{}
	handlers   []*handler
	endpoints  []*register.Endpoint
	opts       server.SubscriberOptions
}

func newSubscriber(topic string, sub interface{}, opts ...server.SubscriberOption) server.Subscriber {
	options := server.NewSubscriberOptions(opts...)

	var endpoints []*register.Endpoint
	var handlers []*handler

	if typ := reflect.TypeOf(sub); typ.Kind() == reflect.Func {
		h := &handler{
			method: reflect.ValueOf(sub),
		}

		switch typ.NumIn() {
		case 1:
			h.reqType = typ.In(0)
		case 2:
			h.ctxType = typ.In(0)
			h.reqType = typ.In(1)
		}

		handlers = append(handlers, h)
		ep := &register.Endpoint{
			Name:     "Func",
			Request:  register.ExtractSubValue(typ),
			Metadata: metadata.New(2),
		}
		ep.Metadata.Set("topic", topic)
		ep.Metadata.Set("subscriber", "true")
		endpoints = append(endpoints, ep)
	} else {
		hdlr := reflect.ValueOf(sub)
		name := reflect.Indirect(hdlr).Type().Name()

		for m := 0; m < typ.NumMethod(); m++ {
			method := typ.Method(m)
			h := &handler{
				method: method.Func,
			}

			switch method.Type.NumIn() {
			case 2:
				h.reqType = method.Type.In(1)
			case 3:
				h.ctxType = method.Type.In(1)
				h.reqType = method.Type.In(2)
			}

			handlers = append(handlers, h)
			ep := &register.Endpoint{
				Name:     name + "." + method.Name,
				Request:  register.ExtractSubValue(method.Type),
				Metadata: metadata.New(2),
			}
			ep.Metadata.Set("topic", topic)
			ep.Metadata.Set("subscriber", "true")
			endpoints = append(endpoints, ep)
		}
	}

	return &httpSubscriber{
		rcvr:       reflect.ValueOf(sub),
		typ:        reflect.TypeOf(sub),
		topic:      topic,
		subscriber: sub,
		handlers:   handlers,
		endpoints:  endpoints,
		opts:       options,
	}
}

func (s *httpServer) createSubHandler(sb *httpSubscriber, opts server.Options) broker.Handler {
	return func(p broker.Event) error {
		msg := p.Message()
		ct := msg.Header["Content-Type"]
		cf, err := s.newCodec(ct)
		if err != nil {
			return err
		}

		hdr := metadata.Copy(msg.Header)
		delete(hdr, "Content-Type")
		ctx := metadata.NewIncomingContext(context.Background(), hdr)

		results := make(chan error, len(sb.handlers))

		for i := 0; i < len(sb.handlers); i++ {
			handler := sb.handlers[i]

			var isVal bool
			var req reflect.Value

			if handler.reqType.Kind() == reflect.Ptr {
				req = reflect.New(handler.reqType.Elem())
			} else {
				req = reflect.New(handler.reqType)
				isVal = true
			}
			if isVal {
				req = req.Elem()
			}

			buf := bytes.NewBuffer(msg.Body)

			if err := cf.ReadHeader(buf, &codec.Message{}, codec.Event); err != nil {
				return err
			}

			if err := cf.ReadBody(buf, req.Interface()); err != nil {
				return err
			}

			fn := func(ctx context.Context, msg server.Message) error {
				var vals []reflect.Value
				if sb.typ.Kind() != reflect.Func {
					vals = append(vals, sb.rcvr)
				}
				if handler.ctxType != nil {
					vals = append(vals, reflect.ValueOf(ctx))
				}

				vals = append(vals, reflect.ValueOf(msg.Payload()))

				returnValues := handler.method.Call(vals)
				if err := returnValues[0].Interface(); err != nil {
					return err.(error)
				}
				return nil
			}

			for i := len(opts.SubWrappers); i > 0; i-- {
				fn = opts.SubWrappers[i-1](fn)
			}

			go func() {
				results <- fn(ctx, &httpMessage{
					topic:       sb.topic,
					contentType: ct,
					payload:     req.Interface(),
					header:      msg.Header,
					body:        msg.Body,
					codec:       cf,
				})
			}()
		}

		var errors []string

		for i := 0; i < len(sb.handlers); i++ {
			if err := <-results; err != nil {
				errors = append(errors, err.Error())
			}
		}

		if len(errors) > 0 {
			return fmt.Errorf("subscriber error: %s", strings.Join(errors, "\n"))
		}

		return nil
	}
}

func (s *httpSubscriber) Topic() string {
	return s.topic
}

func (s *httpSubscriber) Subscriber() interface{} {
	return s.subscriber
}

func (s *httpSubscriber) Endpoints() []*register.Endpoint {
	return s.endpoints
}

func (s *httpSubscriber) Options() server.SubscriberOptions {
	return s.opts
}
