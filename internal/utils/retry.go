package utils

import (
	"time"
)

// RetryOperation retries the given operation function up to maxRetries times with a delay between retries.
// The delay is only taken between attempts — never after the last one, where it
// would add pure latency to an already-failed call (with maxRetries=1 the old
// behavior slept 2s on every failure for nothing).
func RetryOperation(operation func() error, maxRetries int, retryDelay time.Duration) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = operation()
		if err == nil {
			return nil
		}
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}
	return err
}
