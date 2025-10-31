//go:build integration

// The build tag above tells Go to ONLY include this file
// when the 'integration' tag is provided:
//
// go test -v -tags=integration ./...

package docker_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/docker" // Importing our package
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupIntegrationTest initializes a REAL Docker client for integration testing.
// It loads real configuration from .env and YAML files.
func setupIntegrationTest(t *testing.T) docker.ClientInterface {
	// 1. Load REAL configuration
	globalCfg, err := config.NewConfig()
	if err != nil {
		t.Skipf("Skipping integration test: Failed to load config. Is .env present? Error: %s", err)
	}

	dockerCfg := globalCfg.GetDockerConfig()
	require.NotNil(t, dockerCfg, "DockerConfig should not be nil")

	// 2. Use the PRODUCTION constructor
	// This will connect to the real Docker daemon as defined in our config
	client, err := docker.NewDockerClient(dockerCfg, globalCfg)
	require.NoError(t, err, "Failed to connect to Docker daemon. Is Docker running?")
	require.NotNil(t, client, "Client should not be nil")

	return client
}

// TestDockerClient_Integration_Lifecycle tests the full lifecycle
// of creating and removing a container against a real Docker daemon.
func TestDockerClient_Integration_Lifecycle(t *testing.T) {
	// 1. Arrange
	client := setupIntegrationTest(t)
	defer client.Close()

	ctx := context.Background()
	// Use a unique name for the test container
	containerName := fmt.Sprintf("integration_test_%d", time.Now().UnixNano())

	// We test using the calibration replica type
	// This REQUIRES that our 'calibration_config.yaml' points to a valid
	// and pullable image (e.g., "alpine:latest").
	replicaType := config.REPLICA_TYPE_CALIBRATION

	// 2. Act: Create and Start the container
	t.Logf("Attempting to create container '%s'...", containerName)
	id, err := client.CreateAndStartContainer(ctx, containerName, replicaType)

	// 3. Assert: Check for success
	require.NoError(t, err, "Failed to create and start container")
	require.NotEmpty(t, id, "Container ID should not be empty")

	t.Logf("Successfully created container %s (ID: %s)", containerName, id)

	// 4. Cleanup: MUST remove the container
	// t.Cleanup() ensures this runs even if the assertions above fail.
	t.Cleanup(func() {
		t.Logf("Cleaning up container ID: %s", id)
		// Use a new context in case the test context timed out
		removeCtx := context.Background()

		err := client.RemoveContainer(removeCtx, id, true)

		assert.NoError(t, err, "Failed to remove container during cleanup")
		t.Logf("Cleanup complete for container ID: %s", id)
	})
}
