package middleware

import (
	"fmt"
	"math"
	"time"

	"github.com/coordinator-service/src/config"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

// Middleware handles RabbitMQ connections and operations
type Middleware struct {
	conn             *amqp.Connection
	channel          *amqp.Channel
	logger           *logrus.Logger
	MiddlewareConfig *config.MiddlewareConfig
}

// NewMiddleware creates a new middleware with exponential backoff retry logic
func NewMiddleware(cfg *config.MiddlewareConfig) (*Middleware, error) {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	url := fmt.Sprintf("amqp://%s:%s@%s:%d/",
		cfg.GetUsername(), cfg.GetPassword(), cfg.GetHost(), cfg.GetPort())

	var conn *amqp.Connection
	var err error

	// Exponential backoff retry logic
	maxRetries := cfg.GetMaxRetries()
	for attempt := 1; attempt <= maxRetries; attempt++ {
		logger.WithFields(logrus.Fields{
			"attempt":     attempt,
			"max_retries": maxRetries,
			"host":        cfg.GetHost(),
			"port":        cfg.GetPort(),
		}).Info("Attempting to connect to RabbitMQ")

		conn, err = amqp.Dial(url)
		if err == nil {
			break
		}

		if attempt < maxRetries {
			// Calculate exponential backoff: 2^attempt seconds
			backoffSecs := int(math.Pow(2, float64(attempt)))
			logger.WithFields(logrus.Fields{
				"attempt":      attempt,
				"error":        err.Error(),
				"backoff_secs": backoffSecs,
			}).Warn("Failed to connect to RabbitMQ, retrying with exponential backoff")

			time.Sleep(time.Duration(backoffSecs) * time.Second)
		} else {
			logger.WithFields(logrus.Fields{
				"attempt": attempt,
				"error":   err.Error(),
			}).Error("Failed to connect to RabbitMQ after maximum retries")
			return nil, fmt.Errorf("failed to connect to RabbitMQ after %d attempts: %w", maxRetries, err)
		}
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"host": cfg.GetHost(),
		"port": cfg.GetPort(),
		"user": cfg.GetUsername(),
	}).Info("Successfully connected to RabbitMQ")

	return &Middleware{
		conn:             conn,
		channel:          ch,
		logger:           logger,
		MiddlewareConfig: cfg,
	}, nil
}

// SetupTopology declares the required RabbitMQ topology (exchange, queue, binding)
func (m *Middleware) SetupTopology() error {
	// Declare scalability exchange (direct type)
	if err := m.DeclareExchange(config.SCALABILITY_EXCHANGE, "direct"); err != nil {
		return fmt.Errorf("failed to declare scalability exchange: %w", err)
	}
	// Declare scalability queue
	if err := m.DeclareQueue(config.SCALABILITY_QUEUE); err != nil {
		return fmt.Errorf("failed to declare scalability queue: %w", err)
	}

	if err := m.BindQueue(config.SCALABILITY_QUEUE, config.SCALABILITY_EXCHANGE, ""); err != nil {
		return fmt.Errorf("failed to bind scalability queue to exchange: %w", err)
	}
	m.logger.WithFields(logrus.Fields{
		"queue":    config.SCALABILITY_QUEUE,
		"exchange": config.SCALABILITY_EXCHANGE,
	}).Info("Successfully bound queue to scalability exchange")

	return nil
}

// SetQoS configures the prefetch count for the channel
func (mw *Middleware) SetQoS(prefetchCount int) error {
	return mw.channel.Qos(
		prefetchCount, // prefetch count - number of messages without ack
		0,             // prefetch size - 0 means no limit on message size
		false,
	)
}

// DeclareQueue declares a queue with all settings set to false as per requirements
func (m *Middleware) DeclareQueue(queueName string) error {
	_, err := m.channel.QueueDeclare(
		queueName, // name
		false,     // durable
		false,     // delete when unused (autoDelete)
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	return err
}

// DeclareExchange declares an exchange with all settings set to false as per requirements
func (m *Middleware) DeclareExchange(exchangeName string, exchangeType string) error {
	return m.channel.ExchangeDeclare(
		exchangeName,
		exchangeType,
		false, // durable
		false, // autoDelete
		false, // internal
		false, // noWait
		nil,   // arguments
	)
}

// BindQueue binds a queue to an exchange
func (m *Middleware) BindQueue(queueName, exchangeName, routingKey string) error {
	return m.channel.QueueBind(
		queueName,
		routingKey,
		exchangeName,
		false,
		nil,
	)
}

// BasicConsume starts consuming messages from a queue
func (m *Middleware) BasicConsume(queueName string, consumerTag string) (<-chan amqp.Delivery, error) {
	msgs, err := m.channel.Consume(
		queueName,
		consumerTag, // consumer
		false,       // autoAck
		false,       // exclusive
		false,       // noLocal
		false,       // noWait
		nil,         // args
	)
	if err != nil {
		return nil, err
	}

	return msgs, nil
}

// CancelConsumer cancels a consumer
func (m *Middleware) CancelConsumer(consumerTag string) error {
	return m.channel.Cancel(consumerTag, false)
}

// StopConsuming cancels all consumers to stop receiving new messages
func (m *Middleware) StopConsuming(consumerTag string) error {
	m.logger.WithField("consumer_tag", consumerTag).Info("Stopping message consumption")
	return m.CancelConsumer(consumerTag)
}

// Close closes the channel and connection
func (m *Middleware) Close() {
	if m.channel != nil {
		if err := m.channel.Close(); err != nil {
			m.logger.WithError(err).Warn("Failed to close RabbitMQ channel")
		}
	}
	if m.conn != nil {
		if err := m.conn.Close(); err != nil {
			m.logger.WithError(err).Warn("Failed to close RabbitMQ connection")
		}
	}
	m.logger.Info("RabbitMQ connection closed")
}

// Conn returns the underlying connection for reuse
func (m *Middleware) Conn() *amqp.Connection {
	return m.conn
}
