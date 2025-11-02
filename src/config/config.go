package config

import (
	"errors" // <-- Added import
	"fmt"
	"os"
	"path/filepath" // <-- Added import
	"strconv"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

const (
	SCALABILITY_EXCHANGE = "scalability_exchange"
	SCALABILITY_QUEUE    = "scalability_queue"

	// Replica types
	REPLICA_TYPE_CALIBRATION = "calibration"
	REPLICA_TYPE_DISPATCHER  = "dispatcher"
	CONSUMER_TAG             = "coordinator-service"
)

type Interface interface {
	GetLogLevel() string
	GetServiceName() string
	GetContainerName() string
	GetMiddlewareConfig() *MiddlewareConfig
	GetDockerConfig() *DockerConfig
	GetReplicaConfig(replicaType string) (*ReplicaConfig, bool)
	GetWorkerPoolSize() int
}

// GlobalConfig holds all configuration for the service
type GlobalConfig struct {
	logLevel         string
	serviceName      string
	containerName    string
	middlewareConfig *MiddlewareConfig
	dockerConfig     *DockerConfig
	replicaConfigs   map[string]*ReplicaConfig
	workerPoolSize   int
}

// MiddlewareConfig holds RabbitMQ connection configuration
type MiddlewareConfig struct {
	host     string
	port     int32
	username string
	password string
}

// DockerConfig holds Docker connection configuration
type DockerConfig struct {
	host string
}

// ReplicaConfig holds configuration for a replica type
type ReplicaConfig struct {
	Image              ImageConfig         `yaml:"image"`
	RestartPolicy      RestartPolicyConfig `yaml:"restart_policy"`
	EnvVarsUnformatted map[string]string   `yaml:"env_vars"`
	Networks           []string            `yaml:"networks"`
	EnvVarsFormatted   []string            // Pre-built env vars in "KEY=VALUE" format for Docker SDK
}

// ImageConfig holds Docker image information
type ImageConfig struct {
	Name string `yaml:"name"`
	Tag  string `yaml:"tag"`
}

// RestartPolicyConfig holds restart policy configuration
type RestartPolicyConfig struct {
	Name       string `yaml:"name"`
	MaxRetries int    `yaml:"max_retries"`
}

// findProjectRoot searches upwards from the current directory to find
// a marker file (like go.mod) and returns the path to that directory.
// It was necessary for testing purposes
func findProjectRoot(marker string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Loop upwards until we find the marker or hit the root
	for {
		markerPath := filepath.Join(cwd, marker)
		// Check if the marker file exists
		if _, err := os.Stat(markerPath); err == nil {
			return cwd, nil // Found it
		}

		// Go up one directory
		parent := filepath.Dir(cwd)
		if parent == cwd {
			// Reached the root directory without finding it
			return "", errors.New("project root (go.mod) not found")
		}
		cwd = parent
	}
}

// NewConfig loads configuration from environment variables and YAML files
func NewConfig() (GlobalConfig, error) {
	// Find the project root by looking for "go.mod"
	rootPath, err := findProjectRoot("go.mod")
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to find project root: %w", err)
	}

	// Construct the path to the .env file
	envPath := filepath.Join(rootPath, ".env")

	// Load the .env file from the project root
	err = godotenv.Load(envPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return GlobalConfig{}, fmt.Errorf("failed to load .env file from %s: %w", envPath, err)
		}
	}

	// Get RabbitMQ connection details from environment
	rabbitHost := os.Getenv("RABBITMQ_HOST")
	if rabbitHost == "" {
		return GlobalConfig{}, fmt.Errorf("RABBITMQ_HOST environment variable is required")
	}

	rabbitPortStr := os.Getenv("RABBITMQ_PORT")
	if rabbitPortStr == "" {
		return GlobalConfig{}, fmt.Errorf("RABBITMQ_PORT environment variable is required")
	}
	rabbitPort, err := strconv.ParseInt(rabbitPortStr, 10, 32)
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("RABBITMQ_PORT must be a valid integer: %w", err)
	}

	rabbitUser := os.Getenv("RABBITMQ_USER")
	if rabbitUser == "" {
		return GlobalConfig{}, fmt.Errorf("RABBITMQ_USER environment variable is required")
	}

	rabbitPass := os.Getenv("RABBITMQ_PASS")
	if rabbitPass == "" {
		return GlobalConfig{}, fmt.Errorf("RABBITMQ_PASS environment variable is required")
	}

	// Set log level from environment
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info" // Default
	}

	// Get worker pool size from environment (optional with default)
	workerPoolSize := 10
	if poolSizeStr := os.Getenv("WORKER_POOL_SIZE"); poolSizeStr != "" {
		parsed, err := strconv.Atoi(poolSizeStr)
		if err != nil {
			return GlobalConfig{}, fmt.Errorf("WORKER_POOL_SIZE must be a valid integer: %w", err)
		}
		workerPoolSize = parsed
	}

	// Get Docker host from environment (optional with default)
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost == "" {
		dockerHost = "unix:///var/run/docker.sock"
	}

	// Get container name from hostname (automatic detection)
	containerName, err := os.Hostname()
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to get container hostname: %w", err)
	}

	// Load replica configurations from YAML files
	replicaConfigs := make(map[string]*ReplicaConfig)

	calibConfigPath := filepath.Join(rootPath, "calibration_config.yaml")
	calibrationConfig, err := loadReplicaConfig(calibConfigPath)
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to load calibration config: %w", err)
	}
	replicaConfigs["calibration"] = calibrationConfig

	dispConfigPath := filepath.Join(rootPath, "dispatcher_config.yaml")
	dispatcherConfig, err := loadReplicaConfig(dispConfigPath)
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to load dispatcher config: %w", err)
	}
	replicaConfigs["dispatcher"] = dispatcherConfig

	return GlobalConfig{
		logLevel:       logLevel,
		serviceName:    "coordinator-service",
		containerName:  containerName,
		workerPoolSize: workerPoolSize,
		middlewareConfig: &MiddlewareConfig{
			host:     rabbitHost,
			port:     int32(rabbitPort),
			username: rabbitUser,
			password: rabbitPass,
		},
		dockerConfig: &DockerConfig{
			host: dockerHost,
		},
		replicaConfigs: replicaConfigs,
	}, nil
}

// loadReplicaConfig loads a replica configuration from a YAML file
func loadReplicaConfig(filename string) (*ReplicaConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filename, err)
	}

	// Expand environment variables (e.g., "${VAR}")
	expandedData := []byte(os.ExpandEnv(string(data)))

	var config ReplicaConfig
	if err := yaml.Unmarshal(expandedData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", filename, err)
	}

	return &config, nil
}

// GlobalConfig getters
func (c GlobalConfig) GetLogLevel() string {
	return c.logLevel
}

func (c GlobalConfig) GetServiceName() string {
	return c.serviceName
}

func (c GlobalConfig) GetContainerName() string {
	return c.containerName
}

func (c GlobalConfig) GetMiddlewareConfig() *MiddlewareConfig {
	return c.middlewareConfig
}

func (c GlobalConfig) GetDockerConfig() *DockerConfig {
	return c.dockerConfig
}

func (c GlobalConfig) GetReplicaConfig(replicaType string) (*ReplicaConfig, bool) {
	config, exists := c.replicaConfigs[replicaType]
	return config, exists
}

func (c GlobalConfig) GetWorkerPoolSize() int {
	return c.workerPoolSize
}

// MiddlewareConfig getters
func (m MiddlewareConfig) GetHost() string {
	return m.host
}

func (m MiddlewareConfig) GetPort() int32 {
	return m.port
}

func (m MiddlewareConfig) GetUsername() string {
	return m.username
}

func (m MiddlewareConfig) GetPassword() string {
	return m.password
}

// DockerConfig getters
func (d DockerConfig) GetHost() string {
	return d.host
}

// NewMiddlewareConfigForTesting creates a MiddlewareConfig for testing purposes.
// This is exported to allow integration tests to create custom configurations.
func NewMiddlewareConfigForTesting(host string, port int32, username, password string) *MiddlewareConfig {
	return &MiddlewareConfig{
		host:     host,
		port:     port,
		username: username,
		password: password,
	}
}
