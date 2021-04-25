package http

import (
	"io"

	"github.com/unistack-org/micro/v3/codec"
	"github.com/unistack-org/micro/v3/metadata"
)

type rpcRequest struct {
	rw          io.ReadWriter
	payload     interface{}
	codec       codec.Codec
	header      metadata.Metadata
	method      string
	endpoint    string
	contentType string
	service     string
	target      string
	body        []byte
	stream      bool
}

type rpcMessage struct {
	payload     interface{}
	codec       codec.Codec
	header      metadata.Metadata
	topic       string
	contentType string
	body        []byte
}

func (r *rpcRequest) ContentType() string {
	return r.contentType
}

func (r *rpcRequest) Service() string {
	return r.service
}

func (r *rpcRequest) Method() string {
	return r.method
}

func (r *rpcRequest) Endpoint() string {
	return r.method
}

func (r *rpcRequest) Codec() codec.Codec {
	return r.codec
}

func (r *rpcRequest) Header() metadata.Metadata {
	return r.header
}

func (r *rpcRequest) Read() ([]byte, error) {
	f := &codec.Frame{}
	if err := r.codec.ReadBody(r.rw, f); err != nil {
		return nil, err
	}
	return f.Data, nil
}

func (r *rpcRequest) Stream() bool {
	return r.stream
}

func (r *rpcRequest) Body() interface{} {
	return r.payload
}

func (r *rpcMessage) ContentType() string {
	return r.contentType
}

func (r *rpcMessage) Topic() string {
	return r.topic
}

func (r *rpcMessage) Payload() interface{} {
	return r.payload
}

func (r *rpcMessage) Header() metadata.Metadata {
	return r.header
}

func (r *rpcMessage) Body() []byte {
	return r.body
}

func (r *rpcMessage) Codec() codec.Codec {
	return r.codec
}
