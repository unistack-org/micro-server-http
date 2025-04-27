package http

import (
	"context"
	"testing"
)

func TestHandler(t *testing.T) {
	ctx := context.WithValue(context.TODO(), rspCodeKey{}, &rspCodeVal{})
	SetRspCode(ctx, 404)
}
