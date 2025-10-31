package mocks

import (
	"context"

	"github.com/coordinator-service/src/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/mock"
)

// Middleware is a mock implementation of the middleware.Interface
type Middleware struct {
	mock.Mock
}

// Ensure Middleware implements middleware.Interface
var _ middleware.Interface = (*Middleware)(nil)

// --- Simulated methods ---

func (m *Middleware) SetupTopology() error {
	args := m.Called()
	return args.Error(0)
}

func (m *Middleware) SetQoS(prefetchCount int) error {
	args := m.Called(prefetchCount)
	return args.Error(0)
}

func (m *Middleware) BasicConsume(queueName string, consumerTag string) (<-chan amqp.Delivery, error) {
	args := m.Called(queueName, consumerTag)

	// return the channels as specified in the test setup
	return args.Get(0).(<-chan amqp.Delivery), args.Error(1)
}

func (m *Middleware) StopConsuming(consumerTag string) error {
	args := m.Called(consumerTag)
	return args.Error(0)
}

func (m *Middleware) Close() {
	m.Called() // Does not return anything
}

func (m *Middleware) NotifyClose(receiver chan *amqp.Error) chan *amqp.Error {
	args := m.Called(receiver)
	return args.Get(0).(chan *amqp.Error)
}

func (m *Middleware) TryConnect(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// --- End of simulated methods ---
