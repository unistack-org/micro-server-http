package http

import (
	"github.com/unistack-org/micro/v3/codec"
	"github.com/unistack-org/micro/v3/metadata"
)

type httpMessage struct {
	payload     interface{}
	codec       codec.Codec
	header      metadata.Metadata
	topic       string
	contentType string
	body        []byte
}

func (r *httpMessage) Topic() string {
	return r.topic
}

func (r *httpMessage) Payload() interface{} {
	return r.payload
}

func (r *httpMessage) ContentType() string {
	return r.contentType
}

func (r *httpMessage) Header() metadata.Metadata {
	return r.header
}

func (r *httpMessage) Body() []byte {
	return r.body
}

func (r *httpMessage) Codec() codec.Codec {
	return r.codec
}
