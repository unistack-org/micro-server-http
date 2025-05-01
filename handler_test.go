package http

import (
	"context"
	"testing"
)

func TestHandler(t *testing.T) {
	ctx := context.WithValue(context.TODO(), rspStatusCodeKey{}, &rspStatusCodeVal{})
	SetResponseStatusCode(ctx, 404)
}
