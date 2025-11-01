// En middleware_test.go

package middleware_test

import (
	"context"
	"testing"
	"time"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMiddleware_TryConnect_ContextCancellation
// tests that TryConnect respects context cancellation
func TestMiddleware_TryConnect_ContextCancellation(t *testing.T) {
	cfg := config.NewMiddlewareConfigForTesting(
		"localhost",
		9999, // closed port to force connection failure
		"guest",
		"guest",
	)

	mw, err := middleware.NewMiddleware(cfg)
	require.NoError(t, err)

	//  create a context that will timeout quickly
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// we expect TryConnect to fail due to context timeout
	err = mw.TryConnect(ctx)

	require.Error(t, err, "TryConnect should fail due to context cancellation")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "Error should be context deadline exceeded")
}
