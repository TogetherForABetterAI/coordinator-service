package mocks

import (
	"github.com/coordinator-service/src/middleware" // Importa la interfaz real
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/mock"
)

// Middleware es un mock para la interfaz middleware.Interface
type Middleware struct {
	mock.Mock
}

// Aseguramos en tiempo de compilación que nuestro mock implementa la interfaz
var _ middleware.Interface = (*Middleware)(nil)

// SetupTopology simula la llamada a la interfaz real
func (m *Middleware) SetupTopology() error {
	args := m.Called()
	return args.Error(0)
}

// SetQoS simula la llamada a la interfaz real
func (m *Middleware) SetQoS(prefetchCount int) error {
	args := m.Called(prefetchCount)
	return args.Error(0)
}

// BasicConsume simula la llamada a la interfaz real
func (m *Middleware) BasicConsume(queueName string, consumerTag string) (<-chan amqp.Delivery, error) {
	args := m.Called(queueName, consumerTag)
    
    // Devolvemos los canales que nos digan en la configuración del test
	return args.Get(0).(<-chan amqp.Delivery), args.Error(1)
}

// StopConsuming simula la llamada a la interfaz real
func (m *Middleware) StopConsuming(consumerTag string) error {
	args := m.Called(consumerTag)
	return args.Error(0)
}

// Close simula la llamada a la interfaz real
func (m *Middleware) Close() {
	m.Called() // No devuelve nada
}