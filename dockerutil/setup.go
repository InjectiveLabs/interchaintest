package dockerutil

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
)

// DockerSetupTestingT is a subset of testing.T required for DockerSetup.
type DockerSetupTestingT interface {
	Helper()

	Name() string

	Failed() bool
	Cleanup(func())

	Logf(format string, args ...any)
}

// CleanupLabel is a docker label key targeted by DockerSetup when it cleans up docker resources.
//
// "interchaintest" is perhaps a better name. However, for backwards compatibility we preserve the original name of "ibc-test"
// with the hyphen. Otherwise, we run the risk of causing "container already exists" errors because DockerSetup
// is unable to clean old resources from docker engine.
const CleanupLabel = "ibc-test"

// CleanupLabel is the "old" format.
// Note that any new labels should follow the reverse DNS format suggested at
// https://docs.docker.com/config/labels-custom-metadata/#key-format-recommendations.

const (
	// LabelPrefix is the reverse DNS format "namespace" for interchaintest Docker labels.
	LabelPrefix = "ventures.strangelove.interchaintest."

	// NodeOwnerLabel indicates the logical node owning a particular object (probably a volume).
	NodeOwnerLabel = LabelPrefix + "node-owner"
)

// KeepVolumesOnFailure determines whether volumes associated with a test
// using DockerSetup are retained or deleted following a test failure.
//
// The value is false by default, but can be initialized to true by setting the
// environment variable ICTEST_SKIP_FAILURE_CLEANUP to a non-empty value.
// Alternatively, importers of the dockerutil package may set the variable to true.
// Because dockerutil is an internal package, the public API for setting this value
// is interchaintest.KeepDockerVolumesOnFailure(bool).
var KeepVolumesOnFailure = os.Getenv("ICTEST_SKIP_FAILURE_CLEANUP") != ""

// DockerSetup returns a new Docker Client and the ID of a configured network, associated with t.
//
// If any part of the setup fails, DockerSetup panics because the test cannot continue.
func DockerSetup(t DockerSetupTestingT) (*client.Client, string) {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(fmt.Errorf("failed to create docker client: %v", err))
	}

	// Clean up docker resources at end of test, if enabled also collects coverage data.
	t.Cleanup(DockerCleanup(t, cli, DockerExportCoverageDataFn(t, cli)))

	// Also eagerly clean up any leftover resources from a previous test run,
	// e.g. if the test was interrupted. No coverage data is exported in this case.
	DockerCleanup(t, cli, nil)()

	name := fmt.Sprintf("%s-%s", ICTDockerPrefix, RandLowerCaseLetterString(8))
	network, err := cli.NetworkCreate(context.TODO(), name, types.NetworkCreate{
		CheckDuplicate: true,

		Labels: map[string]string{CleanupLabel: t.Name()},
	})
	if err != nil {
		panic(fmt.Errorf("failed to create docker network: %v", err))
	}

	return cli, network.ID
}

// DockerExportCoverageData guarantees the cleanup, but also exports coverage data from the containers beforehand.
func DockerExportCoverageDataFn(t DockerSetupTestingT, cli *client.Client) func() {
	return func() {
		defer func() {
			if e := recover(); e != nil {
				t.Logf("Failed to export coverage data: %v", e)
				return
			}
		}()

		outCoverageDataDir := os.Getenv("ICTEST_GOCOVERDIR")
		if outCoverageDataDir == "" {
			outCoverageDataDir = "coverage/" + t.Name()
		}

		ctx := context.TODO()
		cli.NegotiateAPIVersion(ctx)
		cs, err := cli.ContainerList(ctx, types.ContainerListOptions{
			All: true,
			Filters: filters.NewArgs(
				filters.Arg("label", CleanupLabel+"="+t.Name()),
			),
		})
		if err != nil {
			t.Logf("Failed to list containers during docker export coverage data: %v", err)
			return
		}

		for _, c := range cs {
			var coverageDataDir string

			// Get GOCOVERDIR environment variable from container
			containerInspect, err := cli.ContainerInspect(ctx, c.ID)
			if err != nil {
				t.Logf("Failed to inspect container %s: %v", c.ID, err)
				continue
			}

			for _, env := range containerInspect.Config.Env {
				if strings.HasPrefix(env, "GOCOVERDIR=") {
					coverageDataDir = strings.TrimPrefix(env, "GOCOVERDIR=")
					break
				}
			}

			// coverage data not enabled for export
			if coverageDataDir == "" {
				continue
			}

			containerName := c.ID[:12]
			if len(c.Names) > 0 {
				containerName = c.Names[0]
			}

			t.Logf("Exporting coverage data %s from container %s", coverageDataDir, containerName)

			// Copy coverage data from container to local filesystem
			reader, _, err := cli.CopyFromContainer(ctx, c.ID, coverageDataDir)
			if err != nil {
				t.Logf("Failed to copy coverage data from container %s: %v", c.ID, err)
				continue
			}
			defer reader.Close()

			// Create full path for coverage data
			containerCoverageDataDir := filepath.Join(outCoverageDataDir, containerName)
			if err := os.MkdirAll(containerCoverageDataDir, 0755); err != nil {
				t.Logf("Failed to create coverage data directory for container %s: %v", c.ID, err)
				continue
			}

			// Extract the tar archive containing coverage data
			tr := tar.NewReader(reader)
			for {
				header, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Logf("Failed to read tar header from container %s: %v", c.ID, err)
					break
				}

				// Skip directories
				if header.Typeflag == tar.TypeDir {
					continue
				}

				// Create coverage data file
				outPath := filepath.Join(containerCoverageDataDir, filepath.Base(header.Name))
				outFile, err := os.Create(outPath)
				if err != nil {
					t.Logf("Failed to create coverage data file %s: %v", outPath, err)
					continue
				}
				defer outFile.Close()

				if _, err := io.Copy(outFile, tr); err != nil {
					t.Logf("Failed to write coverage data file %s: %v", outPath, err)
				}
			}
		}
	}
}

// DockerCleanup will clean up Docker containers, networks, and the other various config files generated in testing.
func DockerCleanup(t DockerSetupTestingT, cli *client.Client, preRemoveCallback func()) func() {
	return func() {
		showContainerLogs := os.Getenv("SHOW_CONTAINER_LOGS")
		containerLogTail := os.Getenv("CONTAINER_LOG_TAIL")
		keepContainers := os.Getenv("KEEP_CONTAINERS") != ""

		ctx := context.TODO()
		cli.NegotiateAPIVersion(ctx)
		cs, err := cli.ContainerList(ctx, types.ContainerListOptions{
			All: true,
			Filters: filters.NewArgs(
				filters.Arg("label", CleanupLabel+"="+t.Name()),
			),
		})
		if err != nil {
			t.Logf("Failed to list containers during docker cleanup: %v", err)
			return
		}

		for _, c := range cs {
			if (t.Failed() && showContainerLogs == "") || showContainerLogs == "always" {
				logTail := "1000"
				if containerLogTail != "" {
					logTail = containerLogTail
				}
				rc, err := cli.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
					ShowStdout: true,
					ShowStderr: true,
					Tail:       logTail,
				})
				if err == nil {
					b := new(bytes.Buffer)
					_, err := b.ReadFrom(rc)
					if err == nil {
						t.Logf("\n\nContainer logs - {%s}\n%s", strings.Join(c.Names, " "), b.String())
					}
				}
			}
			if !keepContainers {
				var stopTimeout container.StopOptions
				timeout := 10
				timeoutDur := time.Duration(timeout * int(time.Second))
				deadline := time.Now().Add(timeoutDur)
				stopTimeout.Timeout = &timeout
				if err := cli.ContainerStop(ctx, c.ID, stopTimeout); IsLoggableStopError(err) {
					t.Logf("Failed to stop container %s during docker cleanup: %v", c.ID, err)
				}

				waitCtx, cancel := context.WithDeadline(ctx, deadline.Add(500*time.Millisecond))
				waitCh, errCh := cli.ContainerWait(waitCtx, c.ID, container.WaitConditionNotRunning)
				select {
				case <-waitCtx.Done():
					t.Logf("Timed out waiting for container %s", c.ID)
				case err := <-errCh:
					t.Logf("Failed to wait for container %s during docker cleanup: %v", c.ID, err)
				case res := <-waitCh:
					if res.Error != nil {
						t.Logf("Error while waiting for container %s during docker cleanup: %s", c.ID, res.Error.Message)
					}
					// Ignoring statuscode for now.
				}
				cancel()

				// Export coverage data from the container before removing it.
				if preRemoveCallback != nil {
					preRemoveCallback()
				}

				if err := cli.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{
					// Not removing volumes with the container, because we separately handle them conditionally.
					Force: true,
				}); err != nil {
					t.Logf("Failed to remove container %s during docker cleanup: %v", c.ID, err)
				}
			}
		}

		if !keepContainers {
			PruneVolumesWithRetry(ctx, t, cli)
			PruneNetworksWithRetry(ctx, t, cli)
		} else {
			t.Logf("Keeping containers - Docker cleanup skipped")
		}
	}
}

func PruneVolumesWithRetry(ctx context.Context, t DockerSetupTestingT, cli *client.Client) {
	if KeepVolumesOnFailure && t.Failed() {
		return
	}

	var msg string
	err := retry.Do(
		func() error {
			res, err := cli.VolumesPrune(ctx, filters.NewArgs(filters.Arg("label", CleanupLabel+"="+t.Name())))
			if err != nil {
				if errdefs.IsConflict(err) {
					// Prune is already in progress; try again.
					return err
				}

				// Give up on any other error.
				return retry.Unrecoverable(err)
			}

			if len(res.VolumesDeleted) > 0 {
				msg = fmt.Sprintf("Pruned %d volumes, reclaiming approximately %.1f MB", len(res.VolumesDeleted), float64(res.SpaceReclaimed)/(1024*1024))
			}

			return nil
		},
		retry.Context(ctx),
		retry.DelayType(retry.FixedDelay),
	)
	if err != nil {
		t.Logf("Failed to prune volumes during docker cleanup: %v", err)
		return
	}

	if msg != "" {
		// Odd to Logf %s, but this is a defensive way to keep the DockerSetupTestingT interface
		// with only Logf and not need to add Log.
		t.Logf("%s", msg)
	}
}

func PruneNetworksWithRetry(ctx context.Context, t DockerSetupTestingT, cli *client.Client) {
	var deleted []string
	err := retry.Do(
		func() error {
			res, err := cli.NetworksPrune(ctx, filters.NewArgs(filters.Arg("label", CleanupLabel+"="+t.Name())))
			if err != nil {
				if errdefs.IsConflict(err) {
					// Prune is already in progress; try again.
					return err
				}

				// Give up on any other error.
				return retry.Unrecoverable(err)
			}

			deleted = res.NetworksDeleted
			return nil
		},
		retry.Context(ctx),
		retry.DelayType(retry.FixedDelay),
	)
	if err != nil {
		t.Logf("Failed to prune networks during docker cleanup: %v", err)
		return
	}

	if len(deleted) > 0 {
		t.Logf("Pruned unused networks: %v", deleted)
	}
}

func IsLoggableStopError(err error) bool {
	if err == nil {
		return false
	}
	return !(errdefs.IsNotModified(err) || errdefs.IsNotFound(err))
}
