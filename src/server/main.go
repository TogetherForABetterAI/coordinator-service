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

type Server struct {
	config           config.Interface
	middleware       middleware.Interface
	dockerClient     docker.ClientInterface
	rabbitmqListener *listener.RabbitMQListener
	dockerListener   *listener.DockerListener
	logger           *logrus.Logger
	ctx              context.Context
	cancel           context.CancelFunc
}

func NewServer(cfg config.Interface) (*Server, error) {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	mw, err := middleware.NewMiddleware(cfg.GetMiddlewareConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create middleware: %w", err)
	}

	dockerClient, err := docker.NewDockerClient(cfg.GetDockerConfig(), cfg)
	if err != nil {
		mw.Close()
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	rabbitmqListener := listener.NewRabbitMQListener(mw, dockerClient, cfg)
	dockerListener := listener.NewDockerListener(dockerClient)

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
	}, nil
}

func (s *Server) Start() {
	s.logger.Info("Starting server listeners...")

	s.rabbitmqListener.Start()
	s.dockerListener.Start()

	s.logger.Info("Server started successfully")
}

func (s *Server) Stop() {
	s.logger.Info("Initiating graceful server shutdown")

	s.rabbitmqListener.Stop()
	s.dockerListener.Stop()
	s.rabbitmqListener.Wait()
	s.dockerListener.Wait()

	s.middleware.Close()

	if err := s.dockerClient.Close(); err != nil {
		s.logger.WithError(err).Warn("Failed to close Docker client")
	}

	s.logger.Info("Server shutdown completed")
}
