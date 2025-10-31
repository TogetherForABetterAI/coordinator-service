package listener

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/docker"
	customerrors "github.com/coordinator-service/src/errors"
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
	consumerTag  string
	closeChan    chan *amqp.Error // Channel to receive connection close notifications

	// Context and cancellation for graceful shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// NewRabbitMQListener creates a new "cold" RabbitMQ listener.
func NewRabbitMQListener(
	mw middleware.Interface,
	dockerClient docker.ClientInterface,
	cfg config.Interface,
) *RabbitMQListener {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	workerPool := make(chan struct{}, cfg.GetWorkerPoolSize())
	logger.WithField("worker_pool_size", cfg.GetWorkerPoolSize()).Info("RabbitMQ listener initialized")

	// Create an internal context for managing the listener's lifecycle
	ctx, cancel := context.WithCancel(context.Background())

	return &RabbitMQListener{
		middleware:   mw,
		dockerClient: dockerClient,
		config:       cfg,
		logger:       logger,
		workerPool:   workerPool,
		ctx:          ctx,
		cancel:       cancel,
		consumerTag:  config.CONSUMER_TAG,
	}
}

// Run starts the connection monitor goroutine.
// This should be called once
func (l *RabbitMQListener) Start() {
	l.logger.Info("Starting RabbitMQ connection monitor...")

	l.closeChan = make(chan *amqp.Error, 1)
	l.middleware.NotifyClose(l.closeChan)

	l.wg.Add(1)
	go l.monitorMiddlewareConnection()

	// We send a "nil" to the close channel to trigger
	// the connection logic inside monitorMiddlewareConnection immediately
	l.closeChan <- nil
}

// monitorMiddlewareConnection monitors the RabbitMQ connection and attempts to connect
func (l *RabbitMQListener) monitorMiddlewareConnection() {
	defer l.wg.Done()
	l.logger.Info("Connection monitor started")

	for {
		select {
		case _, ok := <-l.closeChan:
			if !ok {
				l.logger.Info("Close channel was externally closed, stopping monitor")
				return
			}

			// Stop any active consumption (if it exists)
			l.middleware.StopConsuming(l.consumerTag)

			l.logger.Info("Starting reconnection process...")

			// Attempt to connect with exponential backoff
			if err := l.middleware.TryConnect(l.ctx); err != nil {
				if err == context.Canceled {
					l.logger.Info("Context cancelled, stopping reconnection attempts")
					return
				}
			}

			// Re-register the close listener with the new connection
			l.closeChan = make(chan *amqp.Error, 1)
			l.middleware.NotifyClose(l.closeChan)

			// Start consumption after connection
			if err := l.StartConsuming(); err != nil {
				l.logger.WithError(err).Error("Failed to restart consumption after reconnection")
				// Loop will continue, and next connection drop will trigger again
				continue
			}

			l.logger.Info("Successfully started consumption after connection")

		case <-l.ctx.Done():
			l.logger.Info("Shutdown signal received, stopping connection monitor")
			return
		}
	}
}

// Start begins consuming messages from the scalability queue.
// This method is called by monitorMiddlewareConnection, not by the user.
func (l *RabbitMQListener) StartConsuming() error {
	l.logger.WithField("queue", config.SCALABILITY_QUEUE).Info("Starting to consume from scalability queue")

	if err := l.middleware.SetQoS(l.config.GetWorkerPoolSize()); err != nil {
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	msgs, err := l.middleware.BasicConsume(config.SCALABILITY_QUEUE, l.consumerTag)
	if err != nil {
		return fmt.Errorf("failed to start consuming: %w", err)
	}

	// Process messages in a goroutine
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		l.logger.Info("Message processing loop started")
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					l.logger.Warn("Message channel closed (likely due to connection loss)")
					return // Exit goroutine
				}

				// processMessage returns false if context was cancelled
				if !l.processMessage(msg) {
					l.logger.Info("Context cancelled during message processing, stopping immediately")
					return
				}

			case <-l.ctx.Done():
				l.logger.Info("Context cancelled, stopping message processing loop")
				return
			}
		}
	}()

	return nil
}

// processMessage handles individual messages using the worker pool pattern.
// Returns false if context was cancelled, true otherwise.
func (l *RabbitMQListener) processMessage(msg amqp.Delivery) bool {
	// if pool is full we wait
	// if pool is not full, we proceed normally
	// if a shutdown signal is received, we return false to signal stop.
	select {
	case l.workerPool <- struct{}{}:
	case <-l.ctx.Done():
		return false // Signal to stop processing
	}

	// Process in goroutine (worker)
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() { <-l.workerPool }() // Release worker back to pool

		var scaleMsg models.ScaleMessage
		if err := json.Unmarshal(msg.Body, &scaleMsg); err != nil {
			l.logger.WithFields(logrus.Fields{
				"error": err.Error(),
				"body":  string(msg.Body),
			}).Error("Failed to unmarshal scale message")
			msg.Nack(false, false) // Don't requeue invalid messages
			return
		}

		if err := l.createReplica(l.ctx, scaleMsg.ReplicaType); err != nil {
			requeue := customerrors.IsTransientDockerError(err)
			l.logger.WithFields(logrus.Fields{
				"replica_type": scaleMsg.ReplicaType,
				"error":        err.Error(),
				"requeue":      requeue,
			}).Error("Failed to create replica")
			msg.Nack(false, requeue)
			return
		}

		msg.Ack(false)
	}()

	return true // Continue processing messages
}

// createReplica (no changes from your version)
func (l *RabbitMQListener) createReplica(ctx context.Context, replicaType string) error {
	validTypes := map[string]bool{
		config.REPLICA_TYPE_CALIBRATION: true,
		config.REPLICA_TYPE_DISPATCHER:  true,
	}
	if !validTypes[replicaType] {
		return fmt.Errorf("invalid replica type: %s", replicaType)
	}

	containerName := fmt.Sprintf("%s-%s", replicaType, uuid.New().String())
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

// Wait waits for all workers to finish
func (l *RabbitMQListener) Wait() {
	l.logger.Info("Waiting for RabbitMQ listener workers to finish")
	l.wg.Wait()
	l.logger.Info("All RabbitMQ listener workers finished")
}

// Stop signals the listener to shut down gracefully.
func (l *RabbitMQListener) Stop() {
	l.logger.Info("Shutting down RabbitMQ listener...")
	l.middleware.StopConsuming(l.consumerTag)
	l.cancel() // Signal context cancellation
}
