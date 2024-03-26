package meter

import (
	"context"
	"testing"

	codecpb "go.unistack.org/micro-proto/v4/codec"
)

func TestHandler_Metrics(t *testing.T) {
	type fields struct {
		opts Options
	}
	type args struct {
		ctx context.Context
		req *codecpb.Frame
		rsp *codecpb.Frame
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			"Test #1",
			fields{
				opts: NewOptions(),
			},
			args{
				context.Background(),
				&codecpb.Frame{Data: []byte("gzip")},
				&codecpb.Frame{},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				opts: tt.fields.opts,
			}
			if err := h.Metrics(tt.args.ctx, tt.args.req, tt.args.rsp); (err != nil) != tt.wantErr {
				t.Errorf("Metrics() error = %v, wantErr %v", err, tt.wantErr)
			}
			t.Logf("RSP: %v", tt.args.rsp.Data)
		})
	}
}
