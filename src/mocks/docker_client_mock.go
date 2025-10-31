package mocks

import (
	"context"

	"github.com/coordinator-service/src/docker" // Importa la interfaz real
	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/mock"
)

// DockerClient es un mock para la interfaz docker.ClientInterface
type DockerClient struct {
	mock.Mock // Embeddeamos el mock de testify
}

// Aseguramos en tiempo de compilación que nuestro mock implementa la interfaz
var _ docker.ClientInterface = (*DockerClient)(nil)

// CreateAndStartContainer simula la llamada a la interfaz real
func (m *DockerClient) CreateAndStartContainer(ctx context.Context, containerName string, replicaType string) (string, error) {
	// m.Called() registra que la función fue llamada y con qué argumentos
	args := m.Called(ctx, containerName, replicaType)

	// .Get(0) es el primer valor de retorno (string)
	// .Get(1) es el segundo valor de retorno (error)
	return args.String(0), args.Error(1)
}

// RemoveContainer simula la llamada a la interfaz real
func (m *DockerClient) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

// StreamEvents simula la llamada a la interfaz real
func (m *DockerClient) StreamEvents(ctx context.Context) (<-chan events.Message, <-chan error) {
	args := m.Called(ctx)
	
    // Devolvemos los canales que nos digan en la configuración del test
	return args.Get(0).(<-chan events.Message), args.Get(1).(<-chan error)
}

// Close simula la llamada a la interfaz real
func (m *DockerClient) Close() error {
	args := m.Called()
	return args.Error(0)
}