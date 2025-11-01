//go:build !integration

package listener

import (
	"context"
	"testing"

	"github.com/coordinator-service/src/mocks"
	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/mock"
)

func TestDockerListener_handleEvent(t *testing.T) {
	t.Parallel()

	makeListener := func() (*DockerListener, *mocks.DockerClient) {
		mockDocker := new(mocks.DockerClient)
		l := NewDockerListener(mockDocker)
		return l, mockDocker
	}

	t.Run("ignores non-die events", func(t *testing.T) {
		l, mockDocker := makeListener()
		ev := events.Message{
			Action: "start", // not "die"
			Actor: events.Actor{
				ID: "abc123",
				Attributes: map[string]string{
					"exitCode": "0", // random
					"name":     "calibration-1",
				},
			},
		}

		l.handleEvent(context.Background(), ev)

		mockDocker.AssertNotCalled(t, "RemoveContainer", mock.Anything, mock.Anything)
	})

	t.Run("die with exitCode 0 -> removes container", func(t *testing.T) {
		l, mockDocker := makeListener()
		ev := events.Message{
			Action: "die",
			Actor: events.Actor{
				ID: "abc123",
				Attributes: map[string]string{
					"exitCode": "0",
					"name":     "calibration-1",
				},
			},
		}

		mockDocker.
			On("RemoveContainer", mock.Anything, "abc123").
			Return(nil).
			Once()

		l.handleEvent(context.Background(), ev)

		mockDocker.AssertExpectations(t)
	})

	t.Run("die with exitCode != 0 -> does not remove", func(t *testing.T) {
		l, mockDocker := makeListener()
		ev := events.Message{
			Action: "die",
			Actor: events.Actor{
				ID: "abc123",
				Attributes: map[string]string{
					"exitCode": "2",
					"name":     "calibration-1",
				},
			},
		}

		l.handleEvent(context.Background(), ev)

		mockDocker.AssertNotCalled(t, "RemoveContainer", mock.Anything, mock.Anything)
	})
}
