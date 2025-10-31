package middleware

import (
	"fmt"
	"time"

	"github.com/coordinator-service/src/config"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

// Interface defines the contract for RabbitMQ middleware operations
type Interface interface {
	SetupTopology() error
	SetQoS(prefetchCount int) error
	BasicConsume(queueName string, consumerTag string) (<-chan amqp.Delivery, error)
	StopConsuming(consumerTag string) error
	Close()
	NotifyClose(receiver chan *amqp.Error) chan *amqp.Error
	TryConnect() error
}

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

	// Create empty middleware instance
	mw := &Middleware{
		logger:           logger,
		MiddlewareConfig: cfg,
	}

	// Try to connect (infinite retries with exponential backoff)
	if err := mw.TryConnect(); err != nil {
		// This should never happen since tryConnect loops infinitely
		// until it connects successfully
		return nil, err
	}

	logger.WithFields(logrus.Fields{
		"host": cfg.GetHost(),
		"port": cfg.GetPort(),
		"user": cfg.GetUsername(),
	}).Info("Successfully connected to RabbitMQ")

	return mw, nil
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

// NotifyClose registers a listener for connection close events
func (m *Middleware) NotifyClose(receiver chan *amqp.Error) chan *amqp.Error {
	return m.conn.NotifyClose(receiver)
}


// tryConnect attempts to connect to RabbitMQ with exponential backoff (no max retries)
func (m *Middleware) TryConnect() error {
	backoff := time.Second
	maxBackoff := 5 * time.Minute
	attempt := 1

	for {
		m.logger.WithFields(logrus.Fields{
			"attempt": attempt,
			"backoff": backoff,
		}).Info("Attempting to reconnect to RabbitMQ")

		// Close old connections if they exist
		if m.channel != nil {
			m.channel.Close()
		}
		if m.conn != nil {
			m.conn.Close()
		}

		// Build connection URL
		cfg := m.MiddlewareConfig
		url := fmt.Sprintf("amqp://%s:%s@%s:%d/",
			cfg.GetUsername(), cfg.GetPassword(), cfg.GetHost(), cfg.GetPort())

		// Attempt connection
		conn, err := amqp.Dial(url)
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"attempt": attempt,
				"error":   err.Error(),
				"backoff": backoff,
			}).Warn("Failed to reconnect to RabbitMQ, retrying with exponential backoff")

			time.Sleep(backoff)

			// Exponential backoff with cap
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}

			attempt++
			continue
		}

		// Create channel
		ch, err := conn.Channel()
		if err != nil {
			conn.Close()
			m.logger.WithFields(logrus.Fields{
				"attempt": attempt,
				"error":   err.Error(),
				"backoff": backoff,
			}).Warn("Failed to create channel, retrying with exponential backoff")

			time.Sleep(backoff)

			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}

			attempt++
			continue
		}

		// Update references
		m.conn = conn
		m.channel = ch

		// Setup topology
		if err := m.SetupTopology(); err != nil {
			ch.Close()
			conn.Close()
			m.logger.WithFields(logrus.Fields{
				"attempt": attempt,
				"error":   err.Error(),
				"backoff": backoff,
			}).Warn("Failed to setup topology, retrying with exponential backoff")

			time.Sleep(backoff)

			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}

			attempt++
			continue
		}

		m.logger.WithFields(logrus.Fields{
			"attempt": attempt,
			"host":    cfg.GetHost(),
			"port":    cfg.GetPort(),
		}).Info("Successfully reconnected to RabbitMQ")

		return nil
	}
}
