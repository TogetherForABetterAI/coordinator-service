//go:build integration

// The build tag above tells Go to ONLY include this file
// when the 'integration' tag is provided:
//
// go test -v -tags=integration ./...

package middleware_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

// Global variable to hold the shared middleware config for all tests
var testMiddlewareConfig *config.MiddlewareConfig

// TestMain sets up a single RabbitMQ container for ALL tests in this package,
// runs all tests, and then tears down the container.
func TestMain(m *testing.M) {
	ctx := context.Background()

	// --- SETUP: Start RabbitMQ container ONCE ---
	fmt.Println("Starting RabbitMQ container for integration tests...")

	rabbitmqContainer, err := rabbitmq.Run(ctx,
		"rabbitmq:3.13-management-alpine",
		rabbitmq.WithAdminUsername("guest"),
		rabbitmq.WithAdminPassword("guest"),
	)
	if err != nil {
		fmt.Printf("Failed to start RabbitMQ container: %s\n", err)
		os.Exit(1)
	}

	// Get host and port from container
	host, err := rabbitmqContainer.Host(ctx)
	if err != nil {
		fmt.Printf("Failed to get container host: %s\n", err)
		os.Exit(1)
	}

	mappedPort, err := rabbitmqContainer.MappedPort(ctx, "5672/tcp")
	if err != nil {
		fmt.Printf("Failed to get mapped port: %s\n", err)
		os.Exit(1)
	}

	connectionString, _ := rabbitmqContainer.AmqpURL(ctx)
	fmt.Printf("RabbitMQ container ready at: %s (host=%s, port=%d)\n",
		connectionString, host, mappedPort.Int())

	// Create the shared middleware config that ALL tests will use
	testMiddlewareConfig = config.NewMiddlewareConfigForTesting(
		host,
		int32(mappedPort.Int()),
		"guest",
		"guest",
	)

	// --- RUN ALL TESTS ---
	exitCode := m.Run()

	// --- TEARDOWN: Terminate container ONCE ---
	fmt.Println("Terminating RabbitMQ container...")
	if err := rabbitmqContainer.Terminate(ctx); err != nil {
		fmt.Printf("Failed to terminate RabbitMQ container: %v\n", err)
	}

	// Exit with the test suite's exit code
	os.Exit(exitCode)
}

// TestMiddleware_NotifyClose_SignalPropagation tests that NotifyClose properly
// propagates connection close events after a successful TryConnect
func TestMiddleware_NotifyClose_SignalPropagation(t *testing.T) {
	t.Parallel()

	// Create middleware instance using the shared global config
	mw, err := middleware.NewMiddleware(testMiddlewareConfig)
	require.NoError(t, err, "Failed to create middleware")
	defer mw.Close()

	// Connect to RabbitMQ
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // set a timeout for connection attempts
	defer cancel()

	err = mw.TryConnect(ctx)
	require.NoError(t, err, "Failed to connect to RabbitMQ")

	// Register NotifyClose listener with buffered channel
	closeChan := make(chan *amqp.Error, 1)
	mw.NotifyClose(closeChan)

	// Explicitly close the underlying connection to trigger the close notification
	err = mw.Conn().Close()
	assert.NoError(t, err, "Failed to close connection")

	// Wait for the close notification with timeout
	select {
	case closeErr := <-closeChan:
		// We received a notification (could be nil for graceful close or an error)
		t.Logf("Received close notification: %v", closeErr)
		assert.Nil(t, closeErr, "A graceful close (mw.Conn.Close) should propagate a nil error")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for close notification")
	}
}

// TestMiddleware_GracefulShutdown tests that Close properly shuts down
// connection and channel resources
func TestMiddleware_GracefulShutdown(t *testing.T) {
	t.Parallel()

	// Create middleware instance using the shared global config
	mw, err := middleware.NewMiddleware(testMiddlewareConfig)
	require.NoError(t, err, "Failed to create middleware")

	// Connect to RabbitMQ
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // set a timeout for connection attempts
	defer cancel()

	err = mw.TryConnect(ctx)
	require.NoError(t, err, "Failed to connect to RabbitMQ")

	// Verify connection is active
	assert.NotNil(t, mw.Conn(), "Connection should not be nil")
	assert.NotNil(t, mw.Channel(), "Channel should not be nil")
	assert.False(t, mw.Conn().IsClosed(), "Connection should be open")

	// Call Close to gracefully shutdown
	mw.Close()

	// Verify connection is closed
	assert.True(t, mw.Conn().IsClosed(), "Connection should be closed after Close()")

	// Attempt to use the channel should fail with amqp.ErrClosed
	_, err = mw.Channel().QueueDeclare(
		"test-queue-after-close",
		false, false, false, false, nil,
	)
	assert.Error(t, err, "Operations on closed channel should fail")
	assert.Contains(t, err.Error(), "channel/connection is not open",
		"Error should indicate channel/connection is closed")
}

// TestMiddleware_BasicConsume_And_StopConsuming tests the lifecycle of consuming a message
// and then stopping the consumer.
func TestMiddleware_BasicConsume_And_StopConsuming(t *testing.T) {
	t.Parallel()

	// --- Setup ---
	mw, err := middleware.NewMiddleware(testMiddlewareConfig)
	require.NoError(t, err, "Failed to create middleware")
	defer mw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err = mw.TryConnect(ctx)
	require.NoError(t, err, "Failed to connect to RabbitMQ")

	consumerTag := "test-consumer"
	testBody := []byte("hello-world")

	deliveries, err := mw.BasicConsume(config.SCALABILITY_QUEUE, consumerTag)
	require.NoError(t, err, "Failed to start BasicConsume")

	// Publish a test message
	err = publishTestMessage(mw.Channel(), config.SCALABILITY_EXCHANGE, testBody)
	require.NoError(t, err, "Failed to publish test message")

	// Wait to receive the message
	select {
	case msg := <-deliveries:
		t.Logf("Received message: %s", string(msg.Body))
		assert.Equal(t, testBody, msg.Body, "Message body does not match")
		err = msg.Ack(false) // always Ack in a test
		assert.NoError(t, err, "Failed to Ack message")

	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for message")
	}

	// cancel consuming
	err = mw.StopConsuming(consumerTag)
	require.NoError(t, err, "Failed to stop consuming")

	// verify that the deliveries channel is closed
	// canceling a consumer causes the amqp library to close the channel.
	// Reading from a closed channel returns the "zero value" (nil) and ok=false.
	select {
	case msg, ok := <-deliveries:
		assert.False(t, ok, "Deliveries channel should be closed after StopConsuming")
		assert.Nil(t, msg.Body, "No message should be received from a closed channel")
		t.Log("Successfully verified consumer channel is closed")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for deliveries channel to close")
	}
}

// publishTestMessage is a helper to publish a message to the given exchange
// so that our consumer can receive it.
func publishTestMessage(ch *amqp.Channel, exchange string, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return ch.PublishWithContext(ctx,
		exchange, // exchange
		"",       // routing key
		false,    // mandatory
		false,    // immediate
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        body,
		},
	)
}
