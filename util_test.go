package http

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"go.unistack.org/micro/v3/metadata"
	"go.unistack.org/micro/v3/options"
	"go.unistack.org/micro/v3/server"
)

func Test_Hook(t *testing.T) {
	opts := server.Options{}

	var fn server.HandlerFunc = func(fctx context.Context, req server.Request, rsp interface{}) (err error) {
		// fmt.Println("1")
		return nil
	}

	var fn2 server.HandlerWrapper = func(next server.HandlerFunc) server.HandlerFunc {
		return func(ctx context.Context, req server.Request, rsp interface{}) error {
			//	fmt.Println("2")
			return next(ctx, req, rsp)
		}
	}
	var fn3 server.HandlerWrapper = func(next server.HandlerFunc) server.HandlerFunc {
		return func(ctx context.Context, req server.Request, rsp interface{}) error {
			// fmt.Println("3")
			return next(ctx, req, rsp)
		}
	}
	var fn4 server.HandlerWrapper = func(next server.HandlerFunc) server.HandlerFunc {
		return func(ctx context.Context, req server.Request, rsp interface{}) error {
			// fmt.Println("4")
			return next(ctx, req, rsp)
		}
	}

	opts.Hooks = append(opts.Hooks, fn2, fn3, fn4)

	opts.Hooks.EachNext(func(hook options.Hook) {
		if h, ok := hook.(server.HandlerWrapper); ok {
			// fmt.Printf("h %#+v\n", h)
			fn = h(fn)
		}
	})

	err := fn(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFillrequest(t *testing.T) {
	md := metadata.New(1)
	md.Set("ClientID", "xxx")
	type request struct {
		Token    string
		ClientID string
	}
	ctx := context.Background()
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1", nil)
	cookie1 := &http.Cookie{Name: "Token", Value: "zzz"}
	cookie2 := &http.Cookie{Name: "Token", Value: "zzz"}
	hreq.AddCookie(cookie1)
	hreq.AddCookie(cookie2)

	buf := bytes.NewBuffer(nil)
	_ = hreq.Write(buf)
	var cookie string
	var line string
	var err error
	for {
		line, err = buf.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "Cookie") {
			cookie = strings.TrimSpace(strings.Split(line, ":")[1])
			break
		}
	}

	md.Set("Cookie", cookie)
	ctx = metadata.NewIncomingContext(ctx, md)
	req := &request{}

	if err := FillRequest(ctx, req, Cookie("Token", "true"), Header("ClientID", "true")); err != nil {
		t.Fatal(err)
	}
	if req.ClientID != "xxx" {
		t.Fatalf("FillRequest error: %#+v", req)
	}
	if req.Token != "zzz" {
		t.Fatalf("FillRequest error: %#+v", req)
	}
}
