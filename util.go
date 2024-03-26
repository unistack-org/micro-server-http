package http

import (
	"context"
	"net/http"
	"strings"

	"go.unistack.org/micro/v4/metadata"
	rutil "go.unistack.org/micro/v4/util/reflect"
)

func FillRequest(ctx context.Context, req interface{}, opts ...FillRequestOption) error {
	var err error
	options := handlerOptions{}
	for _, o := range opts {
		o(&options)
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}

	for idx := 0; idx < len(options.headers)/2; idx += 2 {
		k := http.CanonicalHeaderKey(options.headers[idx])
		v, ok := md[k]
		if !ok {
			continue
		}
		if err = rutil.SetFieldByPath(req, v, k); err != nil {
			return err
		}
	}

	cookies := md["Cookie"]
	cmd := make(map[string]string, len(cookies))
	kv := strings.Split(cookies, "=")
	if len(kv) != 2 {
		return nil
	}
	cmd[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	for idx := 0; idx < len(options.cookies)/2; idx += 2 {
		k := http.CanonicalHeaderKey(options.cookies[idx])
		v, ok := cmd[k]
		if !ok {
			continue
		}
		if err = rutil.SetFieldByPath(req, v, k); err != nil {
			return err
		}
	}

	return nil
}
