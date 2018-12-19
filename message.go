package http

type httpMessage struct {
	topic       string
	contentType string
	payload     interface{}
}

func (r *httpMessage) ContentType() string {
	return r.contentType
}

func (r *httpMessage) Topic() string {
	return r.topic
}

func (r *httpMessage) Payload() interface{} {
	return r.payload
}
