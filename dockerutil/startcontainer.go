package dockerutil

import (
	"context"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/docker/docker/api/types/container"
	"github.com/moby/moby/client"
)

const dockerCliStartMandatoryDeadline = 30 * time.Second
const dockerCliStartRetries = 50
const dockerCliStartDelay = 500 * time.Millisecond

// StartContainer attempts to start the container with the given ID.
func StartContainer(ctx context.Context, cli *client.Client, id string) error {
	// add a deadline for the request if the calling context does not provide one
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, dockerCliStartMandatoryDeadline)
		defer cancel()
	}

	if err := retry.Do(func() error {
		return cli.ContainerStart(ctx, id, container.StartOptions{})
	},
		retry.Attempts(dockerCliStartRetries),
		retry.Delay(dockerCliStartDelay),
		retry.Context(ctx),
		retry.RetryIf(isRetryableContainerStartError),
		retry.LastErrorOnly(true),
	); err != nil {
		return err
	}

	return nil
}

func isRetryableContainerStartError(err error) bool {
	if err == nil {
		return false
	}

	// Docker/containerd can transiently fail to mount a just-created
	// container's overlayfs rootfs while another short-lived helper container is
	// being torn down. Retrying only this daemon-side EBUSY condition avoids
	// masking permanent image, command, permission, or config errors.
	return strings.Contains(strings.ToLower(err.Error()), "device or resource busy")
}
