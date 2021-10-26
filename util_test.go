package http

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"go.unistack.org/micro/v3/metadata"
)

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
