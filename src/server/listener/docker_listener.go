package listener

import (
	"context"
	"strconv"
	"sync"

	"github.com/coordinator-service/src/docker"
	"github.com/docker/docker/api/types/events"
	"github.com/sirupsen/logrus"
)

// DockerListener monitors Docker events and handles container cleanup
type DockerListener struct {
	dockerClient docker.ClientInterface
	logger       *logrus.Logger
	wg           sync.WaitGroup
}

// NewDockerListener creates a new Docker event listener
func NewDockerListener(dockerClient docker.ClientInterface) *DockerListener {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	return &DockerListener{
		dockerClient: dockerClient,
		logger:       logger,
	}
}

// Start begins monitoring Docker events
func (dl *DockerListener) Start(ctx context.Context) error {
	dl.logger.Info("Starting Docker event listener")

	// Start streaming events
	eventChan, errChan := dl.dockerClient.StreamEvents(ctx)

	// Process events in a goroutine
	dl.wg.Add(1)
	go func() {
		defer dl.wg.Done()
		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					dl.logger.Info("Event channel closed, stopping Docker event listener")
					return
				}
				dl.handleEvent(ctx, event)

			case err, ok := <-errChan:
				if !ok {
					dl.logger.Info("Error channel closed, stopping Docker event listener")
					return
				}
				if err != nil {
					dl.logger.WithError(err).Error("Error streaming Docker events")
				}
				return

			case <-ctx.Done():
				dl.logger.Info("Context cancelled, stopping Docker event listener")
				return
			}
		}
	}()

	return nil
}

// handleEvent processes individual Docker events
func (dl *DockerListener) handleEvent(ctx context.Context, event events.Message) {
	// We're filtering for "die" events in the Docker client
	if event.Action != "die" {
		return
	}

	// Get container ID
	containerID := event.Actor.ID

	// Get exit code from event attributes
	exitCodeStr, exists := event.Actor.Attributes["exitCode"]
	if !exists {
		dl.logger.WithField("container_id", containerID).Warn("No exit code found in event")
		return
	}

	exitCode, err := strconv.Atoi(exitCodeStr)
	if err != nil {
		dl.logger.WithFields(logrus.Fields{
			"container_id": containerID,
			"exit_code":    exitCodeStr,
			"error":        err.Error(),
		}).Error("Failed to parse exit code")
		return
	}

	// Only remove containers that exited cleanly (exit code 0)
	if exitCode == 0 {
		if err := dl.dockerClient.RemoveContainer(ctx, containerID, false); err != nil {
			dl.logger.WithFields(logrus.Fields{
				"container_id": containerID,
				"error":        err.Error(),
			}).Error("Failed to remove container")
		} else {
			dl.logger.WithFields(logrus.Fields{
				"container_id":   containerID,
				"container_name": event.Actor.Attributes["name"],
			}).Info("Container removed successfully")
		}
	} else {
		dl.logger.WithFields(logrus.Fields{
			"container_id":   containerID,
			"container_name": event.Actor.Attributes["name"],
			"exit_code":      exitCode,
		}).Info("Container exited with non-zero code, not removing")
	}
}

// Stop gracefully stops the listener
func (dl *DockerListener) Wait() {
	dl.wg.Wait()
}
