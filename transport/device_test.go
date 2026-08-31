package transport

import (
	"context"
	"testing"
)

func TestDeviceIDFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want *string
	}{
		{
			name: "anonymous request has no device",
			ctx:  context.Background(),
			want: nil,
		},
		{
			name: "resolved device is returned",
			ctx:  context.WithValue(context.Background(), deviceContextKey{}, "01JABCDEF0123456789ABCDEFG"),
			want: strPtr("01JABCDEF0123456789ABCDEFG"),
		},
		{
			// The middleware never stores an empty ID, but treating one as
			// anonymous keeps requireDevice from handing out a blank identity.
			name: "empty device ID is treated as anonymous",
			ctx:  context.WithValue(context.Background(), deviceContextKey{}, ""),
			want: nil,
		},
		{
			name: "wrong value type is ignored",
			ctx:  context.WithValue(context.Background(), deviceContextKey{}, 42),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deviceIDFromContext(tt.ctx)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("deviceIDFromContext() = %q, want nil", *got)
			case tt.want != nil && got == nil:
				t.Errorf("deviceIDFromContext() = nil, want %q", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("deviceIDFromContext() = %q, want %q", *got, *tt.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
