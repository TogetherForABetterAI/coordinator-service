//go:build integration

// The build tag above tells Go to ONLY include this file
// when the 'integration' tag is provided:
//
// go test -v -tags=integration ./...

package docker_test // Using _test package for black-box testing

import (
	"context"
	"fmt"
	"strings" // Import for Contains
	"testing"
	"time"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/docker" // Importing our package
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupIntegrationTest initializes a REAL Docker client for integration testing.
// It loads real configuration from .env and YAML files.
func setupIntegrationTest(t *testing.T, configModifier func(config.Interface)) (docker.ClientInterface, config.Interface) {
	// 1. Load REAL configuration
	globalCfg, err := config.NewConfig()
	if err != nil {
		t.Skipf("Skipping integration test: Failed to load config. Is .env present? Error: %s", err)
	}

	// 2. Apply modifications (to config) if provided
	if configModifier != nil {
		configModifier(globalCfg)
	}

	dockerCfg := globalCfg.GetDockerConfig()
	require.NotNil(t, dockerCfg, "DockerConfig should not be nil")

	// 3. Use the PRODUCTION constructor
	client, err := docker.NewDockerClient(dockerCfg, globalCfg)
	require.NoError(t, err, "Failed to connect to Docker daemon. Is Docker running?")
	require.NotNil(t, client, "Client should not be nil")

	return client, globalCfg
}

// TestDockerClient_Integration_Lifecycle tests the full lifecycle (happy path)
// of creating and removing a container against a real Docker daemon.
func TestDockerClient_Integration_Lifecycle(t *testing.T) {
	// 1. Arrange
	client, _ := setupIntegrationTest(t, nil)
	defer client.Close()

	ctx := context.Background()
	containerName := fmt.Sprintf("integration_test_happy_%d", time.Now().UnixNano())
	replicaType := config.REPLICA_TYPE_CALIBRATION

	// 2. Act: Create and Start the container
	t.Logf("Attempting to create container '%s'...", containerName)
	id, err := client.CreateAndStartContainer(ctx, containerName, replicaType)

	// 3. Assert: Check for success
	require.NoError(t, err, "Failed to create and start container")
	require.NotEmpty(t, id, "Container ID should not be empty")

	t.Logf("Successfully created container %s (ID: %s)", containerName, id)

	// 4. Cleanup: MUST remove the container
	t.Cleanup(func() {
		t.Logf("Cleaning up container ID: %s", id)
		removeCtx := context.Background()
		err := client.RemoveContainer(removeCtx, id, true) // Force remove
		assert.NoError(t, err, "Failed to remove container during cleanup")
		t.Logf("Cleanup complete for container ID: %s", id)
	})
}

// TestDockerClient_Integration_ImageNotFound tests the failure case (sad path)
// where the image name in the configuration does not exist.
func TestDockerClient_Integration_ImageNotFound(t *testing.T) {
	// 1. Arrange - with config modification
	client, _ := setupIntegrationTest(t, func(cfg config.Interface) {
		calibConfig, exists := cfg.GetReplicaConfig(config.REPLICA_TYPE_CALIBRATION)
		require.True(t, exists, "Calibration config type not found")

		// Overwrite the image name with one that is guaranteed not to exist
		calibConfig.Image.Name = "nonexistent-repo-for-testing/nonexistent-image"
		calibConfig.Image.Tag = "1.2.3.4.5-invalid"
	})
	defer client.Close()

	// 2. Act
	ctx := context.Background()
	containerName := fmt.Sprintf("integration_test_fail_%d", time.Now().UnixNano())
	replicaType := config.REPLICA_TYPE_CALIBRATION

	t.Logf("Attempting to create container '%s' with non-existent image...", containerName)
	id, err := client.CreateAndStartContainer(ctx, containerName, replicaType)

	// 3. Assert
	require.Error(t, err, "Expected an error when creating container with non-existent image")
	assert.Empty(t, id, "Container ID should be empty on failure")

	t.Logf("Received expected error: %v", err)
	assert.True(t,
		strings.Contains(err.Error(), "No such image") || strings.Contains(err.Error(), "not found"),
		"Error message should indicate the image was not found",
	)
}
