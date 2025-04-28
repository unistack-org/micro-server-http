package http

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.unistack.org/micro/v4/metadata"
)

func TestAppendResponseMetadata(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		md       metadata.Metadata
		expected context.Context
	}{
		{
			name:     "nil metadata",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
			md:       nil,
			expected: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
		},
		{
			name:     "empty metadata",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
			md:       metadata.Metadata{},
			expected: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
		},
		{
			name:     "context without response metadata key",
			ctx:      context.Background(),
			md:       metadata.Pairs("key1", "val1"),
			expected: context.Background(),
		},
		{
			name:     "context with nil response metadata value",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, nil),
			md:       metadata.Pairs("key1", "val1"),
			expected: context.WithValue(context.Background(), rspMetadataKey{}, nil),
		},
		{
			name:     "context with incorrect type in response metadata value",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, struct{}{}),
			md:       metadata.Pairs("key1", "val1"),
			expected: context.WithValue(context.Background(), rspMetadataKey{}, struct{}{}),
		},
		{
			name:     "context with response metadata value, but nil metadata",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: nil}),
			md:       metadata.Pairs("key1", "val1"),
			expected: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: nil}),
		},
		{
			name: "basic metadata append",
			ctx:  context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
			md:   metadata.Pairs("key1", "val1"),
			expected: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{
				m: metadata.Metadata{
					"key1": []string{"val1"},
				},
			}),
		},
		{
			name: "multiple values for same key",
			ctx:  context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
			md:   metadata.Pairs("key1", "val1", "key1", "val2"),
			expected: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{
				m: metadata.Metadata{
					"key1": []string{"val1", "val2"},
				},
			}),
		},
		{
			name: "multiple values for different keys",
			ctx:  context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: metadata.Metadata{}}),
			md:   metadata.Pairs("key1", "val1", "key1", "val2", "key2", "val3", "key2", "val4", "key3", "val5"),
			expected: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{
				m: metadata.Metadata{
					"key1": []string{"val1", "val2"},
					"key2": []string{"val3", "val4"},
					"key3": []string{"val5"},
				},
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			AppendResponseMetadata(tt.ctx, tt.md)
			require.Equal(t, tt.expected, tt.ctx)
		})
	}
}

func TestGetResponseMetadata(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected metadata.Metadata
	}{
		{
			name:     "context without response metadata key",
			ctx:      context.Background(),
			expected: nil,
		},
		{
			name:     "context with nil response metadata value",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, nil),
			expected: nil,
		},
		{
			name:     "context with incorrect type in response metadata value",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, &struct{}{}),
			expected: nil,
		},
		{
			name:     "context with response metadata value, but nil metadata",
			ctx:      context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{m: nil}),
			expected: nil,
		},
		{
			name: "valid metadata",
			ctx: context.WithValue(context.Background(), rspMetadataKey{}, &rspMetadataVal{
				m: metadata.Pairs("key1", "value1"),
			}),
			expected: metadata.Metadata{"key1": {"value1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, getResponseMetadata(tt.ctx))
		})
	}
}
