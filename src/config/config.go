package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

const (
	SCALABILITY_EXCHANGE = "scalability"
	SCALABILITY_QUEUE    = "scalability_queue"

	// Replica types
	REPLICA_TYPE_CALIBRATION = "calibration"
	REPLICA_TYPE_DISPATCHER  = "dispatcher"
)

// GlobalConfig holds all configuration for the service
type GlobalConfig struct {
	logLevel            string
	serviceName         string
	containerName       string
	middlewareConfig    *MiddlewareConfig
	dockerConfig        *DockerConfig
	replicaConfigs      map[string]*ReplicaConfig
	workerPoolSize      int
	shutdownTimeoutSecs int
}

// MiddlewareConfig holds RabbitMQ connection configuration
type MiddlewareConfig struct {
	host       string
	port       int32
	username   string
	password   string
	maxRetries int
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

// NewConfig loads configuration from environment variables and YAML files
func NewConfig() (GlobalConfig, error) {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to load .env file: %w", err)
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

	// Get max retries from environment (optional with default)
	maxRetries := 5
	if retriesStr := os.Getenv("MAX_RETRIES"); retriesStr != "" {
		parsed, err := strconv.Atoi(retriesStr)
		if err != nil {
			return GlobalConfig{}, fmt.Errorf("MAX_RETRIES must be a valid integer: %w", err)
		}
		maxRetries = parsed
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

	// Get shutdown timeout from environment (optional with default)
	shutdownTimeoutSecs := 30
	if timeoutStr := os.Getenv("SHUTDOWN_TIMEOUT_SECS"); timeoutStr != "" {
		parsed, err := strconv.Atoi(timeoutStr)
		if err != nil {
			return GlobalConfig{}, fmt.Errorf("SHUTDOWN_TIMEOUT_SECS must be a valid integer: %w", err)
		}
		shutdownTimeoutSecs = parsed
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

	// Load calibration config
	calibrationConfig, err := loadReplicaConfig("calibration_config.yaml")
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to load calibration config: %w", err)
	}
	replicaConfigs["calibration"] = calibrationConfig

	// Load dispatcher config
	dispatcherConfig, err := loadReplicaConfig("dispatcher_config.yaml")
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to load dispatcher config: %w", err)
	}
	replicaConfigs["dispatcher"] = dispatcherConfig

	return GlobalConfig{
		logLevel:            logLevel,
		serviceName:         "coordinator-service",
		containerName:       containerName,
		workerPoolSize:      workerPoolSize,
		shutdownTimeoutSecs: shutdownTimeoutSecs,
		middlewareConfig: &MiddlewareConfig{
			host:       rabbitHost,
			port:       int32(rabbitPort),
			username:   rabbitUser,
			password:   rabbitPass,
			maxRetries: maxRetries,
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

	var config ReplicaConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
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

func (c GlobalConfig) GetShutdownTimeoutSecs() int {
	return c.shutdownTimeoutSecs
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

func (m MiddlewareConfig) GetMaxRetries() int {
	return m.maxRetries
}

// DockerConfig getters
func (d DockerConfig) GetHost() string {
	return d.host
}
