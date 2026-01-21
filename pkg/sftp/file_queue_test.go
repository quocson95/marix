package sftp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileQueue_ProcessJobs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		queue := NewFileQueue(2)

		jobs := make([]FileJob, 5)
		for i := 0; i < 5; i++ {
			jobs[i] = FileJob{
				Path: "test_path",
				Size: 100,
			}
		}

		var progressCount int32
		var progressBytes int64

		updateFn := func(count int, bytes int64) {
			atomic.StoreInt32(&progressCount, int32(count))
			atomic.StoreInt64(&progressBytes, bytes)
		}

		executor := func(ctx context.Context, job FileJob) (int64, error) {
			time.Sleep(10 * time.Millisecond)
			return job.Size, nil
		}

		err := queue.ProcessJobs(context.Background(), jobs, executor, updateFn)
		if err != nil {
			t.Fatalf("ProcessJobs failed: %v", err)
		}

		if count := atomic.LoadInt32(&progressCount); count != 5 {
			t.Errorf("Expected 5 jobs completed, got %d", count)
		}
		if bytes := atomic.LoadInt64(&progressBytes); bytes != 500 {
			t.Errorf("Expected 500 bytes transferred, got %d", bytes)
		}
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		queue := NewFileQueue(2)
		jobs := make([]FileJob, 5)
		expectedErr := errors.New("simulated error")

		executor := func(ctx context.Context, job FileJob) (int64, error) {
			if job.Path == "fail" {
				return 0, expectedErr
			}
			return 100, nil
		}

		jobs[2].Path = "fail"

		err := queue.ProcessJobs(context.Background(), jobs, executor, nil)
		if err != expectedErr {
			t.Errorf("Expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		queue := NewFileQueue(1)
		jobs := make([]FileJob, 5)

		ctx, cancel := context.WithCancel(context.Background())

		executor := func(ctx context.Context, job FileJob) (int64, error) {
			cancel()
			time.Sleep(50 * time.Millisecond)
			return 0, nil
		}

		err := queue.ProcessJobs(ctx, jobs, executor, nil)
		if err == nil {
			t.Error("Expected error due to context cancellation, got nil")
		}
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	})
}
