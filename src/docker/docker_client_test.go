package docker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/coordinator-service/src/config"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mock SDK Client ---
type MockSDKClient struct {
	mock.Mock
}

func (m *MockSDKClient) Ping(ctx context.Context) (types.Ping, error) {
	args := m.Called(ctx)
	return args.Get(0).(types.Ping), args.Error(1)
}
func (m *MockSDKClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *v1.Platform, containerName string) (container.CreateResponse, error) {
	args := m.Called(ctx, config, hostConfig, networkingConfig, platform, containerName)
	return args.Get(0).(container.CreateResponse), args.Error(1)
}
func (m *MockSDKClient) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}
func (m *MockSDKClient) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}
func (m *MockSDKClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	args := m.Called(ctx, options)
	return args.Get(0).(<-chan events.Message), args.Get(1).(<-chan error)
}
func (m *MockSDKClient) Close() error {
	args := m.Called()
	return args.Error(0)
}
var _ DockerSdk = (*MockSDKClient)(nil)

// --- Mock Global Config ---
type MockGlobalConfig struct {
	mock.Mock
}

func (m *MockGlobalConfig) GetReplicaConfig(replicaType string) (*config.ReplicaConfig, bool) {
	args := m.Called(replicaType)
	if args.Get(0) == nil {
		return nil, args.Bool(1)
	}
	return args.Get(0).(*config.ReplicaConfig), args.Bool(1)
}
func (m *MockGlobalConfig) GetDockerConfig() *config.DockerConfig {
	args := m.Called()
	if args.Get(0) == nil { return nil }
	return args.Get(0).(*config.DockerConfig)
}
func (m *MockGlobalConfig) GetLogLevel() string       { args := m.Called(); return args.String(0) }
func (m *MockGlobalConfig) GetServiceName() string    { args := m.Called(); return args.String(0) }
func (m *MockGlobalConfig) GetContainerName() string  { args := m.Called(); return args.String(0) }
func (m *MockGlobalConfig) GetMiddlewareConfig() *config.MiddlewareConfig {
	args := m.Called()
	if args.Get(0) == nil { return nil }
	return args.Get(0).(*config.MiddlewareConfig)
}
func (m *MockGlobalConfig) GetWorkerPoolSize() int      { args := m.Called(); return args.Int(0) }
func (m *MockGlobalConfig) GetShutdownTimeoutSecs() int { args := m.Called(); return args.Int(0) }
var _ config.Interface = (*MockGlobalConfig)(nil)


// --- Unit Tests ---

// TestNewDockerClient_Logic tests the logic inside the shared newDockerClient constructor
func TestNewDockerClient_Logic(t *testing.T) {
	// Setup para el "Camino Feliz" (Happy Path)
	mockSDK := new(MockSDKClient)
	mockCfg := new(MockGlobalConfig)
	dockerCfg := &config.DockerConfig{}

	// Mock the config dependencies
	calibConfigMock := &config.ReplicaConfig{
		Image:              config.ImageConfig{Name: "calib-img", Tag: "v1"},
		EnvVarsUnformatted: map[string]string{"KEY1": "VALUE1"},
	}
	dispConfigMock := &config.ReplicaConfig{
		Image:              config.ImageConfig{Name: "disp-img", Tag: "v2"},
		EnvVarsUnformatted: map[string]string{},
	}
	
	mockCfg.On("GetReplicaConfig", config.REPLICA_TYPE_CALIBRATION).Return(calibConfigMock, true).Once()
	mockCfg.On("GetReplicaConfig", config.REPLICA_TYPE_DISPATCHER).Return(dispConfigMock, true).Once()

	// Act
	dc, err := MockNewDockerClient(mockSDK, dockerCfg, mockCfg)

	// Aserciones Principales
	require.NoError(t, err, "El constructor no debería fallar en el happy path")
	require.NotNil(t, dc, "El cliente no debería ser nulo en el happy path")

	// --- Sub-Tests Granulares ---
	t.Run("Success - internal fields are set", func(t *testing.T) {
		assert.Equal(t, mockSDK, dc.client, "El SDK mockeado debería ser asignado al campo client")
		assert.Equal(t, dockerCfg, dc.config, "El config de Docker debería ser asignado al campo config")
	})

	t.Run("Success - calibration config is processed", func(t *testing.T) {
		require.Contains(t, dc.replicaConfigs, config.REPLICA_TYPE_CALIBRATION)
		calibConfig := dc.replicaConfigs[config.REPLICA_TYPE_CALIBRATION]
		
		expectedImage := fmt.Sprintf("%s:%s", calibConfigMock.Image.Name, calibConfigMock.Image.Tag)
		assert.Equal(t, expectedImage, fmt.Sprintf("%s:%s", calibConfig.Image.Name, calibConfig.Image.Tag))
		
		require.Len(t, calibConfig.EnvVarsFormatted, 1)
		assert.Equal(t, "KEY1=VALUE1", calibConfig.EnvVarsFormatted[0])
	})

	t.Run("Success - dispatcher config is processed", func(t *testing.T) {
		require.Contains(t, dc.replicaConfigs, config.REPLICA_TYPE_DISPATCHER)
		dispConfig := dc.replicaConfigs[config.REPLICA_TYPE_DISPATCHER]

		expectedImage := fmt.Sprintf("%s:%s", dispConfigMock.Image.Name, dispConfigMock.Image.Tag)
		assert.Equal(t, expectedImage, fmt.Sprintf("%s:%s", dispConfig.Image.Name, dispConfig.Image.Tag))

		assert.Len(t, dispConfig.EnvVarsFormatted, 0)
	})
	
	t.Run("Success - mock expectations met", func(t *testing.T) {
		mockCfg.AssertExpectations(t)
	})


	// --- Test del "Camino Triste" (Sad Path) ---
	t.Run("Failure - Config not found", func(t *testing.T) {
		// 1. Arrange
		sadMockSDK := new(MockSDKClient)
		sadMockCfg := new(MockGlobalConfig)
		sadDockerCfg := &config.DockerConfig{}

		sadMockCfg.On("GetReplicaConfig", config.REPLICA_TYPE_CALIBRATION).Return(nil, false).Once()

		// 2. Act
		dc, err := MockNewDockerClient(sadMockSDK, sadDockerCfg, sadMockCfg)

		// 3. Assert
		require.Error(t, err)
		assert.Nil(t, dc)
		assert.Contains(t, err.Error(), "replica config not found for type: calibration")
		sadMockCfg.AssertExpectations(t)
	})
}

// =========================================================================
// TEST REESCRITO PARA ARREGLAR EL PÁNICO
// =========================================================================

// TestCreateAndStartContainer tests the main business logic in true isolation
func TestCreateAndStartContainer(t *testing.T) {

	// Define test cases
	testCases := []struct {
		name          string
		replicaType   string // Input replica type
		containerName string // Input container name

		// Mock setup for SDK (all we need)
		setupMockSDK func(*MockSDKClient)

		// Expected results
		expectedID     string
		expectedErr    bool
		expectedErrMsg string
	}{
		{
			name:          "Success - Calibration",
			replicaType:   config.REPLICA_TYPE_CALIBRATION,
			containerName: "test-container-calib",
			setupMockSDK: func(sdk *MockSDKClient) {
				// We expect ContainerCreate to be called with the *exact* data
				// from our manually-created replicaConfig map below.
				sdk.On("ContainerCreate",
					context.Background(), // ctx
					// Arg 1: container.Config
					&container.Config{
						Image: "calib-img:v1",
						Env:   []string{"ENV=TEST_CALIB"},
					},
					// Arg 2: container.HostConfig
					&container.HostConfig{
						RestartPolicy: container.RestartPolicy{
							Name:              "on-failure",
							MaximumRetryCount: 5,
						},
					},
					// Arg 3: network.NetworkingConfig
					&network.NetworkingConfig{
						EndpointsConfig: map[string]*network.EndpointSettings{
							"net-a": {},
						},
					},
					(*v1.Platform)(nil),    // platform
					"test-container-calib", // containerName
				).Return(container.CreateResponse{ID: "container-id-123"}, nil).Once()

				// Expect ContainerStart
				sdk.On("ContainerStart", context.Background(), "container-id-123", container.StartOptions{}).
					Return(nil).Once()
			},
			expectedID:  "container-id-123",
			expectedErr: false,
		},
		{
			name:          "Config Not Found",
			replicaType:   "unknown-type", // <-- The logic we are testing
			containerName: "test-container-unknown",
			setupMockSDK: func(sdk *MockSDKClient) {
				// No SDK calls should be made
			},
			expectedErr:    true,
			expectedErrMsg: "replica config not found for type: unknown-type",
		},
		{
			name:          "ContainerCreate Fails",
			replicaType:   config.REPLICA_TYPE_DISPATCHER, // Test with the other type
			containerName: "test-container-disp",
			setupMockSDK: func(sdk *MockSDKClient) {
				// We can use mock.Anything since we aren't testing the *exact* args here
				sdk.On("ContainerCreate",
					mock.Anything, mock.AnythingOfType("*container.Config"),
					mock.Anything, mock.AnythingOfType("*network.NetworkingConfig"),
					mock.Anything, "test-container-disp",
				).Return(container.CreateResponse{}, errors.New("docker create error")).Once()
			},
			expectedErr:    true,
			expectedErrMsg: "failed to create container: docker create error",
		},
		{
			name:          "ContainerStart Fails",
			replicaType:   config.REPLICA_TYPE_CALIBRATION,
			containerName: "test-container-fail-start",
			setupMockSDK: func(sdk *MockSDKClient) {
				// We only care that Create succeeds and Start fails
				sdk.On("ContainerCreate",
					mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
					"test-container-fail-start",
				).Return(container.CreateResponse{ID: "container-id-456"}, nil).Once()

				sdk.On("ContainerStart", mock.Anything, "container-id-456", container.StartOptions{}).
					Return(errors.New("docker start error")).Once()
			},
			expectedErr:    true,
			expectedErrMsg: "failed to start container: docker start error",
		},
	}

	// Run table tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Arrange: Create fresh mocks
			mockSDK := new(MockSDKClient)

			// --- ESTA ES LA SOLUCIÓN ---
			// Construimos manually el struct DockerClient con el estado exacto
			// que queremos probar. Esto AÍSLA el test del constructor.
			dc := &DockerClient{
				client: mockSDK,
				logger: logrus.New(), // Usar un logger real está bien
				config: &config.DockerConfig{},
				replicaConfigs: map[string]*config.ReplicaConfig{
					// Pre-populamos el mapa con el estado "cocinado"
					// que el constructor *habría* creado.
					config.REPLICA_TYPE_CALIBRATION: {
						Image:            config.ImageConfig{Name: "calib-img", Tag: "v1"},
						EnvVarsFormatted: []string{"ENV=TEST_CALIB"},
						RestartPolicy:    config.RestartPolicyConfig{Name: "on-failure", MaxRetries: 5},
						Networks:         []string{"net-a"},
					},
					config.REPLICA_TYPE_DISPATCHER: {
						Image:            config.ImageConfig{Name: "disp-img", Tag: "v2"},
						EnvVarsFormatted: []string{"ENV=TEST_DISP"},
						RestartPolicy:    config.RestartPolicyConfig{Name: "always", MaxRetries: 0},
						Networks:         []string{"net-b"},
					},
				},
			}
			// --- FIN DE LA SOLUCIÓN ---

			// 1b. Set up the test-specific SDK mocks
			tc.setupMockSDK(mockSDK)

			// 2. Act
			// Esta es la función que realmente estamos probando
			id, err := dc.CreateAndStartContainer(context.Background(), tc.containerName, tc.replicaType)

			// 3. Assert
			if tc.expectedErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrMsg)
				assert.Empty(t, id)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedID, id)
			}

			// Verify all expected mock calls were made
			mockSDK.AssertExpectations(t)
		})
	}
}

// TestRemoveContainer (sin cambios)
func TestRemoveContainer(t *testing.T) {
	// 1. Arrange
	mockSDK := new(MockSDKClient)
	// Creamos manualmente el cliente para aislar el test
	dc := &DockerClient{client: mockSDK, logger: logrus.New()}
	
	t.Run("Success", func(t *testing.T) {
		// Arrange
		mockSDK.On("ContainerRemove", mock.Anything, "id-to-remove", container.RemoveOptions{Force: false}).
			Return(nil).Once()
		
		// Act
		err := dc.RemoveContainer(context.Background(), "id-to-remove")

		// Assert
		require.NoError(t, err)
		mockSDK.AssertExpectations(t)
	})

	t.Run("Failure", func(t *testing.T) {
		// Arrange
		mockSDK.On("ContainerRemove", mock.Anything, "id-fails", container.RemoveOptions{Force: false}).
			Return(errors.New("remove error")).Once()

		// Act
		err := dc.RemoveContainer(context.Background(), "id-fails")

		// Assert
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove container: remove error")
		mockSDK.AssertExpectations(t)
	})
}

// TestStreamEvents (FIX APPLIED HERE)
func TestStreamEvents(t *testing.T) {
	// 1. Arrange
	mockSDK := new(MockSDKClient)
	dc := &DockerClient{client: mockSDK, logger: logrus.New()}

	expectedMsgChan := make(chan events.Message)
	expectedErrChan := make(chan error)

	expectedFilters := filters.NewArgs()
	expectedFilters.Add("type", "container")
	expectedFilters.Add("event", "die")
	
	mockSDK.On("Events", mock.Anything, events.ListOptions{Filters: expectedFilters}).
		Return((<-chan events.Message)(expectedMsgChan), (<-chan error)(expectedErrChan)).Once()

	// 2. Act
	msgChan, errChan := dc.StreamEvents(context.Background())

	// 3. Assert
	// FIX: We must cast our bi-directional channels (chan T) to
	// read-only channels (<-chan T) to match the function's return signature.
	assert.Equal(t, (<-chan events.Message)(expectedMsgChan), msgChan)
	assert.Equal(t, (<-chan error)(expectedErrChan), errChan)
	mockSDK.AssertExpectations(t)
}

// TestClose (sin cambios)
func TestClose(t *testing.T) {
	// 1. Arrange
	mockSDK := new(MockSDKClient)
	dc := &DockerClient{client: mockSDK, logger: logrus.New()}

	t.Run("Success", func(t *testing.T) {
		mockSDK.On("Close").Return(nil).Once()
		err := dc.Close()
		require.NoError(t, err)
		mockSDK.AssertExpectations(t)
	})

	t.Run("Failure", func(t *testing.T) {
		mockSDK.On("Close").Return(errors.New("close error")).Once()
		err := dc.Close()
		require.Error(t, err)
		assert.Equal(t, "close error", err.Error())
		mockSDK.AssertExpectations(t)
	})

	t.Run("Nil client", func(t *testing.T) {
		dc.client = nil // Simulate a nil client
		err := dc.Close()
		require.NoError(t, err)
	})
}

