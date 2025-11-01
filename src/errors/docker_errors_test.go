//go:build !integration
package errors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTransientDockerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		isTransient bool
	}{
		// Nil error
		{
			name:        "nil error",
			err:         nil,
			isTransient: false,
		},

		// Transient errors
		{
			name:        "timeout error",
			err:         errors.New("operation timeout"),
			isTransient: true,
		},
		{
			name:        "deadline exceeded",
			err:         errors.New("context deadline exceeded"),
			isTransient: true,
		},
		{
			name:        "connection refused",
			err:         errors.New("connection refused by host"),
			isTransient: true,
		},
		{
			name:        "docker daemon connection",
			err:         errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"),
			isTransient: true,
		},
		{
			name:        "network error",
			err:         errors.New("network error occurred"),
			isTransient: true,
		},
		{
			name:        "temporary failure",
			err:         errors.New("temporary failure in name resolution"),
			isTransient: true,
		},
		{
			name:        "resource temporarily unavailable",
			err:         errors.New("resource temporarily unavailable"),
			isTransient: true,
		},
		{
			name:        "too many requests",
			err:         errors.New("too many requests, please retry"),
			isTransient: true,
		},
		{
			name:        "service unavailable",
			err:         errors.New("service unavailable"),
			isTransient: true,
		},
		{
			name:        "connection reset by peer",
			err:         errors.New("read tcp 127.0.0.1:12345: connection reset by peer"),
			isTransient: true,
		},
		{
			name:        "broken pipe",
			err:         errors.New("write tcp 127.0.0.1:12345: broken pipe"),
			isTransient: true,
		},
		{
			name:        "eof error",
			err:         errors.New("EOF"),
			isTransient: true,
		},
		{
			name:        "unexpected eof",
			err:         errors.New("unexpected EOF reading response"),
			isTransient: true,
		},
		{
			name:        "i/o timeout",
			err:         errors.New("i/o timeout on connection"),
			isTransient: true,
		},
		{
			name:        "no route to host",
			err:         errors.New("no route to host"),
			isTransient: true,
		},

		// Permanent errors
		{
			name:        "invalid replica type",
			err:         errors.New("invalid replica type: unknown"),
			isTransient: false,
		},
		{
			name:        "replica config not found",
			err:         errors.New("replica config not found for type: calibration"),
			isTransient: false,
		},
		{
			name:        "no such image",
			err:         errors.New("No such image: nonexistent/image:latest"),
			isTransient: false,
		},
		{
			name:        "not found generic",
			err:         errors.New("not found"),
			isTransient: false,
		},

		// Unknown errors (default to transient to avoid data loss)
		{
			name:        "unknown error defaults to transient",
			err:         errors.New("some completely unexpected error"),
			isTransient: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTransientDockerError(tt.err)
			assert.Equal(t, tt.isTransient, result, "Error: %v", tt.err)
		})
	}
}

func TestClassifyDockerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected DockerErrorType
	}{
		{
			name:     "transient error",
			err:      errors.New("context deadline exceeded"),
			expected: ErrorTypeTransient,
		},
		{
			name:     "permanent error",
			err:      errors.New("No such image: test:latest"),
			expected: ErrorTypePermanent,
		},
		{
			name:     "unknown error defaults to transient",
			err:      errors.New("weird unexpected error"),
			expected: ErrorTypeTransient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyDockerError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
