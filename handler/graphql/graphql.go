//go:build ignore

package graphql_handler

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"go.unistack.org/micro/v4/logger"
	"go.unistack.org/micro/v4/store"
)

var _ graphql.Cache = (*cacheWrapper)(nil)

type Handler struct {
	opts Options
}

type Option func(*Options)

type Options struct {
	cache *cacheWrapper
	Path  string
}

type cacheWrapper struct {
	s store.Store
	l logger.Logger
}

func (c *cacheWrapper) Get(ctx context.Context, key string) (interface{}, bool) {
	var val interface{}
	if err := c.s.Read(ctx, key, val); err != nil && err != store.ErrNotFound {
		c.l.Error(ctx, fmt.Sprintf("cache.Get %s failed", key), err)
		return nil, false
	}
	return val, true
}

func (c *cacheWrapper) Add(ctx context.Context, key string, val interface{}) {
	if err := c.s.Write(ctx, key, val); err != nil {
		c.l.Error(ctx, fmt.Sprintf("cache.Add %s failed", key), err)
	}
}

func Store(s store.Store) Option {
	return func(o *Options) {
		if o.cache == nil {
			o.cache = &cacheWrapper{}
		}
		o.cache.s = s
	}
}

func Logger(l logger.Logger) Option {
	return func(o *Options) {
		if o.cache == nil {
			o.cache = &cacheWrapper{}
		}
		o.cache.l = l
	}
}

func Path(path string) Option {
	return func(o *Options) {
		o.Path = path
	}
}

func NewHandler(opts ...Option) *Handler {
	options := Options{}
	for _, o := range opts {
		o(&options)
	}
	return &Handler{opts: options}
}
