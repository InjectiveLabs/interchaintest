package dockerutil

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsRetryableContainerStartError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "overlay busy",
			err:  errors.New(`failed to mount overlayfs rootfs: err: device or resource busy`),
			want: true,
		},
		{
			name: "permanent error",
			err:  errors.New("invalid mount config for type bind: bind source path does not exist"),
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isRetryableContainerStartError(tt.err))
		})
	}
}
