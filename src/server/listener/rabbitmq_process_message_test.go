package listener

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/mocks"
	"github.com/coordinator-service/src/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/mock"
)

//
// ----- FAKE CONFIG -----
//

type fakeCfg struct{}

func (fakeCfg) GetWorkerPoolSize() int   { return 2 }
func (fakeCfg) GetContainerName() string { return "test-container" }
func (fakeCfg) GetDockerConfig() *config.DockerConfig {
	// devolvemos un puntero válido a un struct vacío
	return &config.DockerConfig{}
}
func (fakeCfg) GetLogLevel() string {
	return "info"
}
func (fakeCfg) GetMiddlewareConfig() *config.MiddlewareConfig {
	return &config.MiddlewareConfig{} // o un valor adecuado para tu test
}
func (fakeCfg) GetReplicaConfig(replicaType string) (*config.ReplicaConfig, bool) {
	return &config.ReplicaConfig{}, true // o valores adecuados para tu test
}
func (fakeCfg) GetServiceName() string {
	return "test-service"
}

//
// ----- FAKE ACKNOWLEDGER -----
//

type fakeAck struct {
	mock.Mock
}

func (a *fakeAck) Ack(tag uint64, multiple bool) error {
	args := a.Called(tag, multiple)
	return args.Error(0)
}

func (a *fakeAck) Nack(tag uint64, multiple, requeue bool) error {
	args := a.Called(tag, multiple, requeue)
	return args.Error(0)
}

func (a *fakeAck) Reject(tag uint64, requeue bool) error {
	args := a.Called(tag, requeue)
	return args.Error(0)
}

//
// ----- HELPER -----
//

func makeDelivery(body []byte, dtag uint64, ack *fakeAck) amqp.Delivery {
	return amqp.Delivery{
		Body:         body,
		DeliveryTag:  dtag,
		Acknowledger: ack,
	}
}

//
// ----- TESTS -----
//

func TestRabbitMQListener_ProcessMessage(t *testing.T) {
	t.Parallel()

	newListener := func() (*RabbitMQListener, *mocks.Middleware, *mocks.DockerClient, context.Context, context.CancelFunc) {
		mockMW := new(mocks.Middleware)
		mockDocker := new(mocks.DockerClient)
		l := NewRabbitMQListener(mockMW, mockDocker, fakeCfg{})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		return l, mockMW, mockDocker, ctx, cancel
	}

	t.Run("happy path -> create replica and ACK", func(t *testing.T) {
		l, _, mockDocker, ctx, cancel := newListener()
		defer cancel()

		payload, _ := json.Marshal(models.ScaleMessage{ReplicaType: config.REPLICA_TYPE_CALIBRATION})
		ack := &fakeAck{}
		ack.On("Ack", uint64(1), false).Return(nil).Once()

		mockDocker.
			On("CreateAndStartContainer", mock.Anything, mock.AnythingOfType("string"), config.REPLICA_TYPE_CALIBRATION).
			Return("cid-1", nil).
			Once()

		l.processMessage(ctx, makeDelivery(payload, 1, ack))
		l.Wait()

		mockDocker.AssertExpectations(t)
		ack.AssertExpectations(t)
	})

	t.Run("invalid JSON -> NACK(requeue=false)", func(t *testing.T) {
		l, _, mockDocker, ctx, cancel := newListener()
		defer cancel()

		ack := &fakeAck{}
		ack.On("Nack", uint64(2), false, false).Return(nil).Once()

		l.processMessage(ctx, makeDelivery([]byte("{not-json"), 2, ack))
		l.Wait()

		mockDocker.AssertNotCalled(t, "CreateAndStartContainer", mock.Anything, mock.Anything, mock.Anything)
		ack.AssertExpectations(t)
	})

	t.Run("invalid replica type -> NACK discard", func(t *testing.T) {
		l, _, mockDocker, ctx, cancel := newListener()
		defer cancel()

		payload, _ := json.Marshal(models.ScaleMessage{ReplicaType: "bogus"})
		ack := &fakeAck{}
		ack.On("Nack", uint64(3), false, false).Return(nil).Once()

		l.processMessage(ctx, makeDelivery(payload, 3, ack))
		l.Wait()

		mockDocker.AssertNotCalled(t, "CreateAndStartContainer", mock.Anything, mock.Anything, mock.Anything)
		ack.AssertExpectations(t)
	})
}
