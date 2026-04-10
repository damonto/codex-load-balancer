package plus

import (
	"context"
	"time"
)

const purchaseCleanupTimeout = 5 * time.Second

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), purchaseCleanupTimeout)
}
