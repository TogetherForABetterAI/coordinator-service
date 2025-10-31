package errors

import (
	"strings"
)

// DockerErrorType represents the type of Docker error
type DockerErrorType int

const (
	// ErrorTypeTransient represents temporary errors that should be retried
	ErrorTypeTransient DockerErrorType = iota
	// ErrorTypePermanent represents permanent errors that should not be retried
	ErrorTypePermanent
)

// IsTransientDockerError determines if a Docker error is transient (should retry) or permanent (should discard)
func IsTransientDockerError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// Permanent errors - should NOT requeue
	permanentErrors := []string{
		"invalid replica type",     // Invalid configuration
		"replica config not found", // Invalid configuration
		"no such image",            // Image doesn't exist
		"not found",                // Image or resource doesn't exist (when alone)
	}

	for _, permErr := range permanentErrors {
		if strings.Contains(errMsg, permErr) {
			return false
		}
	}

	// Transient errors - SHOULD requeue
	transientErrors := []string{
		"timeout",
		"deadline exceeded",
		"connection refused",
		"network",
		"temporary failure",
		"resource temporarily unavailable",
		"too many requests",
		"service unavailable",
		"cannot connect to the docker daemon",
		"context canceled",
		"connection reset by peer", // TCP connection interrupted
		"broken pipe",              // Connection closed unexpectedly
		"eof",                      // Connection closed (also covers "unexpected eof")
		"i/o timeout",              // Network timeout
		"no route to host",         // Network unreachable
	}

	for _, transErr := range transientErrors {
		if strings.Contains(errMsg, transErr) {
			return true
		}
	}

	// Default: treat unknown errors as transient to avoid data loss
	// This is a safe default - better to retry than lose work
	return true
}

// ClassifyDockerError returns the error type for a given Docker error
func ClassifyDockerError(err error) DockerErrorType {
	if IsTransientDockerError(err) {
		return ErrorTypeTransient
	}
	return ErrorTypePermanent
}
