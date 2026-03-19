package dockerutil

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/moby/moby/errdefs"
	"github.com/stretchr/testify/require"
)

type stubContainerRemover struct {
	err     error
	id      string
	options container.RemoveOptions
}

func (s *stubContainerRemover) ContainerRemove(_ context.Context, id string, options container.RemoveOptions) error {
	s.id = id
	s.options = options
	return s.err
}

func TestRemoveContainerBestEffort(t *testing.T) {
	t.Parallel()

	t.Run("removes with force", func(t *testing.T) {
		t.Parallel()

		stub := &stubContainerRemover{}
		require.NoError(t, removeContainerBestEffort(context.Background(), stub, "container-id"))
		require.Equal(t, "container-id", stub.id)
		require.True(t, stub.options.Force)
		require.False(t, stub.options.RemoveVolumes)
	})

	t.Run("ignores not found", func(t *testing.T) {
		t.Parallel()

		stub := &stubContainerRemover{err: errdefs.NotFound(errors.New("no such container"))}
		require.NoError(t, removeContainerBestEffort(context.Background(), stub, "container-id"))
	})

	t.Run("ignores daemon no such container errors", func(t *testing.T) {
		t.Parallel()

		stub := &stubContainerRemover{err: errors.New("Error response from daemon: No such container: container-id")}
		require.NoError(t, removeContainerBestEffort(context.Background(), stub, "container-id"))
	})

	t.Run("returns other errors", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("boom")
		stub := &stubContainerRemover{err: wantErr}
		require.ErrorIs(t, removeContainerBestEffort(context.Background(), stub, "container-id"), wantErr)
	})
}
