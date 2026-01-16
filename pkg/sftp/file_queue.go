package sftp

import (
	"context"
	"sync"
	"sync/atomic"
)

// FileQueue manages concurrent file transfers
type FileQueue struct {
	concurrency int
	sem         chan struct{}
}

// NewFileQueue creates a new file queue
func NewFileQueue(concurrency int) *FileQueue {
	return &FileQueue{
		concurrency: concurrency,
		sem:         make(chan struct{}, concurrency),
	}
}

// JobExecutor is the function that performs the actual transfer
type JobExecutor func(ctx context.Context, job FileJob) (int64, error)

// ProcessJobs executes a list of jobs concurrently
func (q *FileQueue) ProcessJobs(ctx context.Context, jobs []FileJob, executor JobExecutor, updateFn func(int, int64)) error {
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	var filesDone int64 // atomic
	var bytesDone int64 // atomic

	for _, job := range jobs {
		// Fast fail on error
		if firstErr != nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		q.sem <- struct{}{} // Acquire semaphore
		wg.Add(1)

		go func(j FileJob) {
			defer wg.Done()
			defer func() { <-q.sem }() // Release semaphore

			if ctx.Err() != nil {
				return
			}

			// Execute
			written, err := executor(ctx, j)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
				})
				return // Don't update progress on failure? Or count as failed?
				// For now, simpler to stop.
			}

			// Success
			fd := atomic.AddInt64(&filesDone, 1)
			bd := atomic.AddInt64(&bytesDone, written)

			if updateFn != nil {
				updateFn(int(fd), bd)
			}
		}(job)
	}

	wg.Wait()
	return firstErr
}
