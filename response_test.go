package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetResponseStatusCode(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		code     int
		expected context.Context
	}{
		{
			name:     "context without response status code key",
			ctx:      context.Background(),
			code:     http.StatusOK,
			expected: context.Background(),
		},
		{
			name:     "context with incorrect type in response status code value",
			ctx:      context.WithValue(context.Background(), rspStatusCodeKey{}, struct{}{}),
			code:     http.StatusOK,
			expected: context.WithValue(context.Background(), rspStatusCodeKey{}, struct{}{}),
		},
		{
			name:     "successfully set response status code",
			ctx:      context.WithValue(context.Background(), rspStatusCodeKey{}, &rspStatusCodeVal{}),
			code:     http.StatusOK,
			expected: context.WithValue(context.Background(), rspStatusCodeKey{}, &rspStatusCodeVal{code: http.StatusOK}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetResponseStatusCode(tt.ctx, tt.code)
			require.Equal(t, tt.expected, tt.ctx)
		})
	}
}

func TestGetResponseStatusCode(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected int
	}{
		{
			name:     "no value in context, should return 200",
			ctx:      context.Background(),
			expected: http.StatusOK,
		},
		{
			name:     "context with nil value",
			ctx:      context.WithValue(context.Background(), rspStatusCodeKey{}, nil),
			expected: http.StatusOK,
		},
		{
			name:     "context with wrong type",
			ctx:      context.WithValue(context.Background(), rspStatusCodeKey{}, struct{}{}),
			expected: http.StatusOK,
		},
		{
			name:     "context with valid status code",
			ctx:      context.WithValue(context.Background(), rspStatusCodeKey{}, &rspStatusCodeVal{code: http.StatusNotFound}),
			expected: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, GetResponseStatusCode(tt.ctx))
		})
	}
}
