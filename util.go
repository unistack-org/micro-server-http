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
		k := options.headers[idx]
		v, ok := md.Get(k)
		if !ok {
			continue
		}

		if len(v) == 1 {
			err = rutil.SetFieldByPath(req, v[0], k)
		} else {
			err = rutil.SetFieldByPath(req, v, k)
		}
		if err != nil {
			return err
		}
	}

	cookieVals, _ := md.Get("Cookie")
	for i := range cookieVals {
		cookies := strings.Split(cookieVals[i], ";")
		cmd := make(map[string]string, len(cookies))
		for _, cookie := range cookies {
			kv := strings.Split(cookie, "=")
			if len(kv) != 2 {
				continue
			}
			cmd[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
		for idx := 0; idx < len(options.cookies)/2; idx += 2 {
			k := http.CanonicalHeaderKey(options.cookies[idx])
			v, ok := cmd[k]

			if !ok {
				continue
			}

			err = rutil.SetFieldByPath(req, v, k)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
