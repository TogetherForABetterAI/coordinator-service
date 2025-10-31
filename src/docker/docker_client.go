package docker

import (
	"context"
	"fmt"

	"github.com/coordinator-service/src/config"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
)

type DockerSdk interface {
	Ping(ctx context.Context) (types.Ping, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *v1.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	Close() error
}

// ClientInterface defines the contract for Docker client operations
type ClientInterface interface {
	CreateAndStartContainer(ctx context.Context, containerName string, replicaType string) (string, error)
	RemoveContainer(ctx context.Context, containerID string, force bool) error
	StreamEvents(ctx context.Context) (<-chan events.Message, <-chan error)
	Close() error
}

// DockerClient wraps the Docker client and provides container management operations
type DockerClient struct {
	client         DockerSdk
	logger         *logrus.Logger
	config         *config.DockerConfig
	replicaConfigs map[string]*config.ReplicaConfig
}

// NewDockerClient is the constructor for PRODUCTION
func NewDockerClient(cfg *config.DockerConfig, globalConfig config.Interface) (*DockerClient, error) {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	cli, err := client.NewClientWithOpts(
		client.WithHost(cfg.GetHost()),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Verify connection to Docker daemon
	ctx := context.Background()
	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ping Docker daemon: %w", err)
	}
	logger.WithField("host", cfg.GetHost()).Info("Successfully connected to Docker daemon")

	return newDockerClient(cli, cfg, globalConfig, logger)
}

// MockNewDockerClient is the constructor for TESTING
func MockNewDockerClient(
	cli DockerSdk,
	cfg *config.DockerConfig,
	globalConfig config.Interface,
) (*DockerClient, error) {

	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.Info("Creating DockerClient with MOCKED SDK")

	return newDockerClient(cli, cfg, globalConfig, logger)
}

func newDockerClient(
	cli DockerSdk,
	cfg *config.DockerConfig,
	globalConfig config.Interface,
	logger *logrus.Logger,
) (*DockerClient, error) {

	replicaConfigs := make(map[string]*config.ReplicaConfig)
	replicaTypes := []string{config.REPLICA_TYPE_CALIBRATION, config.REPLICA_TYPE_DISPATCHER}

	for _, replicaType := range replicaTypes {
		replicaConfig, exists := globalConfig.GetReplicaConfig(replicaType)
		if !exists {
			return nil, fmt.Errorf("replica config not found for type: %s", replicaType)
		}

		// Pre-build environment variables
		envFormatted := make([]string, 0, len(replicaConfig.EnvVarsUnformatted))
		for key, value := range replicaConfig.EnvVarsUnformatted {
			envFormatted = append(envFormatted, fmt.Sprintf("%s=%s", key, value))
		}
		replicaConfig.EnvVarsFormatted = envFormatted
		replicaConfigs[replicaType] = replicaConfig

		logger.WithFields(logrus.Fields{
			"replica_type": replicaType,
			"image":        fmt.Sprintf("%s:%s", replicaConfig.Image.Name, replicaConfig.Image.Tag),
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

	// Build network configuration (connect container to all specified networks)
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

	// Create container with all specified configuration
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
func (dc *DockerClient) RemoveContainer(ctx context.Context, containerID string, force bool) error {

	err := dc.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: force, // Only remove if not running
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

	// Start streaming Docker events
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
