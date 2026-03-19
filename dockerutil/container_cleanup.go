package dockerutil

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/moby/moby/errdefs"
)

type containerRemover interface {
	ContainerRemove(context.Context, string, container.RemoveOptions) error
}

func removeContainerBestEffort(ctx context.Context, cli containerRemover, containerID string) error {
	err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	if err != nil && !isContainerNotFoundError(err) {
		return err
	}
	return nil
}

func isContainerNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return errdefs.IsNotFound(err) || strings.Contains(strings.ToLower(err.Error()), "no such container")
}
