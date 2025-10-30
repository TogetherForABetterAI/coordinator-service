package listener

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/docker"
	"github.com/coordinator-service/src/middleware"
	"github.com/coordinator-service/src/models"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

// RabbitMQListener handles consuming messages from RabbitMQ and processing them with a worker pool
type RabbitMQListener struct {
	middleware   middleware.Interface
	dockerClient docker.ClientInterface
	config       config.Interface
	logger       *logrus.Logger
	workerPool   chan struct{} // Semaphore for worker pool
	wg           sync.WaitGroup
	consumerTag  string // Consumer tag for cancelling consumption
}

// NewRabbitMQListener creates a new RabbitMQ listener with a worker pool
func NewRabbitMQListener(
	mw middleware.Interface,
	dockerClient docker.ClientInterface,
	cfg config.Interface,
) *RabbitMQListener {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	// Create worker pool semaphore with fixed size
	workerPool := make(chan struct{}, cfg.GetWorkerPoolSize())

	logger.WithField("worker_pool_size", cfg.GetWorkerPoolSize()).Info("RabbitMQ listener initialized")

	return &RabbitMQListener{
		middleware:   mw,
		dockerClient: dockerClient,
		config:       cfg,
		logger:       logger,
		workerPool:   workerPool,
	}
}

// Start begins consuming messages from the scalability queue
func (l *RabbitMQListener) Start(ctx context.Context) error {
	l.logger.WithField("queue", config.SCALABILITY_QUEUE).Info("Starting to consume from scalability queue")

	// Set consumer tag
	l.consumerTag = "coordinator-service"

	// We pass the worker pool size so we do not get more messages than we can handle
	// This tells rabbitmq: "Only send me N unacked messages at a time"
	if err := l.middleware.SetQoS(l.config.GetWorkerPoolSize()); err != nil {
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	// Start consuming messages
	msgs, err := l.middleware.BasicConsume(config.SCALABILITY_QUEUE, l.consumerTag)
	if err != nil {
		return fmt.Errorf("failed to start consuming: %w", err)
	}

	// Process messages in a goroutine
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					l.logger.Warn("Message channel closed")
					return
				}
				// Process message with worker pool
				l.ProcessMessage(ctx, msg)

			case <-ctx.Done():
				l.logger.Info("Context cancelled, stopping message consumption")
				return
			}
		}
	}()

	return nil
}

// processMessage handles individual messages using the worker pool pattern
func (l *RabbitMQListener) ProcessMessage(ctx context.Context, msg amqp.Delivery) {

	// if pool is full we wait
	// if pool is not full, we proceed normally
	// if a shutdown signal is received, we return in any case.
	select {
	case l.workerPool <- struct{}{}:
	case <-ctx.Done():
		return
	}

	// Process in goroutine (worker)
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() { <-l.workerPool }() // Release worker back to pool

		// Parse message
		var scaleMsg models.ScaleMessage
		if err := json.Unmarshal(msg.Body, &scaleMsg); err != nil {
			l.logger.WithFields(logrus.Fields{
				"error": err.Error(),
				"body":  string(msg.Body),
			}).Error("Failed to unmarshal scale message")
			msg.Nack(false, false) // Don't requeue invalid messages
			return
		}

		// Create container
		if err := l.createReplica(ctx, scaleMsg.ReplicaType); err != nil {
			l.logger.WithFields(logrus.Fields{
				"replica_type": scaleMsg.ReplicaType,
				"error":        err.Error(),
			}).Error("Failed to create replica, sending ACK to discard message")
			// ACK the message to discard it
			msg.Ack(false)
			return
		}

		// ACK message after successful processing
		msg.Ack(false)
	}()
}

// createReplica creates and starts a new container replica
func (l *RabbitMQListener) createReplica(ctx context.Context, replicaType string) error {
	// Validate replica type
	validTypes := map[string]bool{
		config.REPLICA_TYPE_CALIBRATION: true,
		config.REPLICA_TYPE_DISPATCHER:  true,
	}
	if !validTypes[replicaType] {
		return fmt.Errorf("invalid replica type: %s", replicaType)
	}

	// Generate unique container name
	containerName := fmt.Sprintf("%s-%s", replicaType, uuid.New().String())

	l.logger.WithFields(logrus.Fields{
		"replica_type":   replicaType,
		"container_name": containerName,
	}).Info("Creating replica container")

	// Create and start container (environment variables are pre-built in DockerClient)
	containerID, err := l.dockerClient.CreateAndStartContainer(ctx, containerName, replicaType)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	l.logger.WithFields(logrus.Fields{
		"replica_type":   replicaType,
		"container_name": containerName,
		"container_id":   containerID,
	}).Info("Replica container created and started successfully")

	return nil
}

// StopConsuming stops consuming new messages from RabbitMQ
func (l *RabbitMQListener) StopConsuming() {
	l.logger.Info("Stopping RabbitMQ message consumption")
	if err := l.middleware.StopConsuming(l.consumerTag); err != nil {
		l.logger.WithError(err).Warn("Failed to stop consuming")
	}
}

// Wait waits for all workers to finish
func (l *RabbitMQListener) Wait() {
	l.logger.Info("Waiting for RabbitMQ listener workers to finish")
	l.wg.Wait() // wait for all workers to finish
	l.logger.Info("All RabbitMQ listener workers finished")
}
