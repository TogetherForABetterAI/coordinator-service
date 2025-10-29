package docker

import (
	"context"
	"fmt"
	"github.com/coordinator-service/src/config"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

// DockerClient wraps the Docker client and provides container management operations
type DockerClient struct {
	client         *client.Client
	logger         *logrus.Logger
	config         *config.DockerConfig
	replicaConfigs map[string]*config.ReplicaConfig // Single map with all replica configurations including pre-built env vars
} // NewDockerClient creates a new Docker client
func NewDockerClient(cfg *config.DockerConfig, globalConfig config.GlobalConfig) (*DockerClient, error) {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	// Create Docker client
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Verify connection
	ctx := context.Background()
	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ping Docker daemon: %w", err)
	}

	logger.WithField("host", cfg.GetHost()).Info("Successfully connected to Docker daemon")

	// Load and prepare replica configurations
	replicaConfigs := make(map[string]*config.ReplicaConfig)

	replicaTypes := []string{config.REPLICA_TYPE_CALIBRATION, config.REPLICA_TYPE_DISPATCHER}
	for _, replicaType := range replicaTypes {
		replicaConfig, exists := globalConfig.GetReplicaConfig(replicaType)
		if !exists {
			return nil, fmt.Errorf("replica config not found for type: %s", replicaType)
		}

		// Pre-build environment variables in Docker format (KEY=VALUE)
		envFormatted := make([]string, 0, len(replicaConfig.EnvVarsUnformatted))
		for key, value := range replicaConfig.EnvVarsUnformatted {
			envFormatted = append(envFormatted, fmt.Sprintf("%s=%s", key, value))
		}
		replicaConfig.EnvVarsFormatted = envFormatted

		// Store the complete configuration
		replicaConfigs[replicaType] = replicaConfig

		logger.WithFields(logrus.Fields{
			"replica_type": replicaType,
			"image":        fmt.Sprintf("%s:%s", replicaConfig.Image.Name, replicaConfig.Image.Tag),
			"env_count":    len(envFormatted),
		}).Info("Loaded configuration for replica type")
	}

	return &DockerClient{
		client:         cli,
		logger:         logger,
		config:         cfg,
		replicaConfigs: replicaConfigs,
	}, nil
}

// CreateAndStartContainer creates and starts a new container with the given configuration
func (dc *DockerClient) CreateAndStartContainer(ctx context.Context, containerName string, replicaType string) (string, error) {
	// Get replica configuration (contains everything: image, env vars formatted, restart policy, etc.)
	replicaConfig, exists := dc.replicaConfigs[replicaType]
	if !exists {
		return "", fmt.Errorf("replica config not found for type: %s", replicaType)
	}

	imageName := fmt.Sprintf("%s:%s", replicaConfig.Image.Name, replicaConfig.Image.Tag)

	containerConfig := &container.Config{
		Image: imageName,
		Env:   replicaConfig.EnvVarsFormatted,
	}

	// Build host configuration with restart policy
	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{
			Name:              container.RestartPolicyMode(replicaConfig.RestartPolicy.Name),
			MaximumRetryCount: replicaConfig.RestartPolicy.MaxRetries,
		},
	}

	// Build network configuration
	var networkingConfig *network.NetworkingConfig
	if len(replicaConfig.Networks) > 0 {
		endpointsConfig := make(map[string]*network.EndpointSettings)
		for _, networkName := range replicaConfig.Networks {
			endpointsConfig[networkName] = &network.EndpointSettings{}
		}
		networkingConfig = &network.NetworkingConfig{
			EndpointsConfig: endpointsConfig,
		}
	}

	// Create container
	resp, err := dc.client.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		networkingConfig, // Network configuration from YAML
		nil,
		containerName,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := dc.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	dc.logger.WithFields(logrus.Fields{
		"container_id":   resp.ID,
		"container_name": containerName,
	}).Info("Container started successfully")

	return resp.ID, nil
}

// RemoveContainer removes a container by ID
func (dc *DockerClient) RemoveContainer(ctx context.Context, containerID string) error {
	dc.logger.WithField("container_id", containerID).Info("Removing container")

	err := dc.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: false, // Only remove if not running
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	dc.logger.WithField("container_id", containerID).Info("Container removed successfully")
	return nil
}

// StreamEvents streams Docker events filtered for container die events
func (dc *DockerClient) StreamEvents(ctx context.Context) (<-chan events.Message, <-chan error) {
	// Create filters for container die events
	eventFilters := filters.NewArgs()
	eventFilters.Add("type", "container")
	eventFilters.Add("event", "die")

	dc.logger.Info("Starting to stream Docker events")

	// Start streaming events
	eventChan, errChan := dc.client.Events(ctx, events.ListOptions{
		Filters: eventFilters,
	})

	return eventChan, errChan
}

// Close closes the Docker client
func (dc *DockerClient) Close() error {
	if dc.client != nil {
		dc.logger.Info("Closing Docker client")
		return dc.client.Close()
	}
	return nil
}


