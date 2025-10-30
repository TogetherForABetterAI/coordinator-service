package server

import (
	"context"
	"fmt"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/docker"
	"github.com/coordinator-service/src/middleware"
	"github.com/coordinator-service/src/server/listener"
	"github.com/sirupsen/logrus"
)

// Server orchestrates the RabbitMQ and Docker listeners
type Server struct {
	config           config.Interface
	middleware       *middleware.Middleware
	dockerClient     *docker.DockerClient
	rabbitmqListener *listener.RabbitMQListener
	dockerListener   *listener.DockerListener
	logger           *logrus.Logger
	ctx              context.Context
	cancel           context.CancelFunc
}

// NewServer creates a new server instance
func NewServer(cfg config.Interface) (*Server, error) {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	// Create middleware
	mw, err := middleware.NewMiddleware(cfg.GetMiddlewareConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create middleware: %w", err)
	}

	// Setup RabbitMQ topology
	if err := mw.SetupTopology(); err != nil {
		mw.Close()
		return nil, fmt.Errorf("failed to setup RabbitMQ topology: %w", err)
	}

	// Create Docker client
	dockerClient, err := docker.NewDockerClient(cfg.GetDockerConfig(), cfg)
	if err != nil {
		mw.Close()
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Create RabbitMQ listener with worker pool
	rabbitmqListener := listener.NewRabbitMQListener(mw, dockerClient, cfg)

	// Create Docker event listener
	dockerListener := listener.NewDockerListener(dockerClient)

	// Create context for coordinating shutdown
	ctx, cancel := context.WithCancel(context.Background())

	logger.WithFields(logrus.Fields{
		"service":          cfg.GetServiceName(),
		"worker_pool_size": cfg.GetWorkerPoolSize(),
	}).Info("Server initialized successfully")

	return &Server{
		config:           cfg,
		middleware:       mw,
		dockerClient:     dockerClient,
		rabbitmqListener: rabbitmqListener,
		dockerListener:   dockerListener,
		logger:           logger,
		ctx:              ctx,
		cancel:           cancel,
	}, nil
}

// Start starts both listeners concurrently
func (s *Server) Start() error {
	s.logger.Info("Starting coordinator service")

	// Start RabbitMQ listener
	if err := s.rabbitmqListener.Start(s.ctx); err != nil {
		return fmt.Errorf("failed to start RabbitMQ listener: %w", err)
	}
	s.logger.Info("RabbitMQ listener started")

	// Start Docker event listener
	if err := s.dockerListener.Start(s.ctx); err != nil {
		return fmt.Errorf("failed to start Docker listener: %w", err)
	}
	s.logger.Info("Docker listener started")

	s.logger.Info("All listeners started successfully")
	return nil
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	s.logger.Info("Initiating graceful server shutdown")

	// 1. Stop consuming new messages from RabbitMQ
	s.rabbitmqListener.StopConsuming()

	// 2. Cancel context to signal all listeners to stop processing
	s.cancel()

	// 3. Wait for listeners to finish processing current work
	s.rabbitmqListener.Wait()
	s.dockerListener.Wait()

	// 4. Now close RabbitMQ connection
	s.middleware.Close()

	// 5. Close Docker client
	if err := s.dockerClient.Close(); err != nil {
		s.logger.WithError(err).Warn("Failed to close Docker client")
	}

	s.logger.Info("Server shutdown completed")
}
