package http

import (
	"go.unistack.org/micro/v3/codec"
	"go.unistack.org/micro/v3/metadata"
)

type httpMessage struct {
	payload     interface{}
	codec       codec.Codec
	header      metadata.Metadata
	topic       string
	contentType string
}

func (r *httpMessage) Topic() string {
	return r.topic
}

func (r *httpMessage) ContentType() string {
	return r.contentType
}

func (r *httpMessage) Header() metadata.Metadata {
	return r.header
}

func (r *httpMessage) Body() interface{} {
	return r.payload
}

func (r *httpMessage) Codec() codec.Codec {
	return r.codec
}
