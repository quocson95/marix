package sftp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quocson95/marix/pkg/ssh"
	"github.com/quocson95/marix/pkg/storage"
)

// TaskType definitions
type TaskType int

const (
	TaskUploadFile TaskType = iota
	TaskUploadDirectory
	TaskDownloadFile
	TaskDownloadDirectory
)

// TaskState definitions
type TaskState int

const (
	TaskPending TaskState = iota
	TaskScanning
	TaskTransferring
	TaskCompleted
	TaskFailed
	TaskCancelled
)

// Task represents a high-level transfer operation
type Task struct {
	ID       int
	Type     TaskType
	Source   string
	Dest     string
	Name     string
	State    TaskState
	Progress TaskProgress

	// Internal
	ctx        context.Context
	cancel     context.CancelFunc
	jobs       []FileJob
	totalSize  int64
	totalFiles int

	// Speed calculation
	lastBytes int64
	lastCheck time.Time

	err error
}

// TaskProgress holds displayable progress info
type TaskProgress struct {
	TaskID           int
	State            TaskState
	TotalFiles       int
	CompletedFiles   int
	FailedFiles      int
	BytesTransferred int64
	TotalSize        int64
	CurrentSpeed     float64
	Percentage       int
	LastLog          string // Output from underlying engine (e.g. rsync)
	Error            string // Error message if failed
}

// TaskQueue manages concurrent tasks
type TaskQueue struct {
	client    *Client
	sshConfig *ssh.SSHConfig
	settings  *storage.Settings

	maxTasks   int
	tasks      []*Task
	taskChan   chan *Task
	updateChan chan TaskProgress

	// Concurrency control
	sem chan struct{}

	nextID int
	mu     sync.Mutex
}

// NewTaskQueue creates a new task queue
func NewTaskQueue(client *Client, sshConfig *ssh.SSHConfig, settings *storage.Settings, maxTasks int, updateChan chan TaskProgress) *TaskQueue {
	if maxTasks <= 0 {
		maxTasks = 5
	}

	tq := &TaskQueue{
		client:     client,
		sshConfig:  sshConfig,
		settings:   settings,
		maxTasks:   maxTasks,
		tasks:      make([]*Task, 0),
		taskChan:   make(chan *Task, maxTasks*2),
		updateChan: updateChan,
		sem:        make(chan struct{}, maxTasks),
		nextID:     1,
	}

	// Start dispatcher
	go tq.dispatcher()

	return tq
}

// UpdateSettings updates the task queue's settings reference
func (q *TaskQueue) UpdateSettings(settings *storage.Settings) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settings = settings
}

func (q *TaskQueue) QueueTask(taskType TaskType, source, dest, name string) (*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	task := &Task{
		ID:     q.nextID,
		Type:   taskType,
		Source: source,
		Dest:   dest,
		Name:   name,
		State:  TaskPending,
		ctx:    ctx,
		cancel: cancel,
	}
	q.nextID++

	q.tasks = append(q.tasks, task)

	// Notify pending
	q.notify(task)
	log.Printf("[INFO] Queued Task %d: %s (%s -> %s)", task.ID, task.Name, task.Source, task.Dest)

	// Queue it
	select {
	case q.taskChan <- task:
		return task, nil
	default:
		log.Printf("[ERROR] Task queue full, dropping task %d", task.ID)
		return nil, fmt.Errorf("task queue full")
	}
}

func (q *TaskQueue) dispatcher() {
	for task := range q.taskChan {
		// Acquire semaphore
		q.sem <- struct{}{}

		go func(t *Task) {
			defer func() { <-q.sem }()
			q.processTask(t)
		}(task)
	}
}

func (q *TaskQueue) processTask(task *Task) {
	if task.ctx.Err() != nil {
		task.State = TaskCancelled
		q.notify(task)
		log.Printf("[INFO] Task %d (%s) cancelled before start", task.ID, task.Name)
		return
	}
	engine := NewTransferEngine(q.client, q.sshConfig, q.settings)
	// If rsync is enabled and it's a directory transfer, skip scanning and delegate entirely to engine
	useRsync := !q.settings.DisableRsync && (task.Type == TaskUploadDirectory || task.Type == TaskDownloadDirectory)
	var result error
	defer func() {
		if result != nil {
			if task.ctx.Err() != nil {
				task.State = TaskCancelled
				log.Printf("[ERROR] Task %d (%s) cancelled during transfer", task.ID, task.Name)
			} else {
				task.State = TaskFailed
				task.err = result
				log.Printf("[ERROR] Task %d (%s) transfer failed: %v", task.ID, task.Name, result)
			}

		} else {
			task.State = TaskCompleted
			task.Progress.CompletedFiles = task.totalFiles
			task.Progress.BytesTransferred = task.totalSize
			task.Progress.Percentage = 100
			log.Printf("[INFO] Task %d (%s) completed successfully", task.ID, task.Name)
		}
		q.notify(task)
	}()
	if useRsync {
		log.Printf("[INFO] Task %d using Rsync recursive mode", task.ID)
		// Single job for the whole directory
		jobs := []FileJob{{
			Path:     task.Name,
			AbsPath:  task.Source,
			DestPath: task.Dest,
			Size:     0, // Unknown/Recalculate later?
			IsDir:    true,
		}}
		task.totalFiles = 1
		task.totalSize = 0
		task.jobs = jobs
		// Transfer Phase
		task.State = TaskTransferring
		q.notify(task)
		result = engine.UploadFile(task.ctx, task.Source, task.Dest, func(bytes int64, output string) error {
			// For rsync, bytes might be 0, output is the line
			if output != "" {
				q.updateChan <- TaskProgress{
					TaskID:  task.ID,
					State:   task.State,
					LastLog: output,
				}
			}
			return nil
		})
		return
	}

	task.State = TaskScanning
	q.notify(task)
	log.Printf("[INFO] Task %d (%s) scanning started", task.ID, task.Name)

	// Scanner
	scanner := NewDirectoryScanner(q.client)

	var jobs []FileJob
	var err error
	var totalSize int64

	// Scanning Phase
	switch task.Type {
	case TaskUploadDirectory:
		jobs, totalSize, err = scanner.ScanLocal(task.ctx, task.Source, filepath.Dir(task.Dest), func(count int) {
			if count%500 == 0 {
				q.updateChan <- TaskProgress{
					TaskID:  task.ID,
					State:   TaskScanning,
					LastLog: fmt.Sprintf("Scanning... %d files found", count),
				}
			}
		})
	case TaskDownloadDirectory:
		jobs, totalSize, err = scanner.ScanRemote(task.ctx, task.Source, filepath.Dir(task.Dest), func(count int) {
			if count%500 == 0 {
				q.updateChan <- TaskProgress{
					TaskID:  task.ID,
					State:   TaskScanning,
					LastLog: fmt.Sprintf("Scanning... %d files found", count),
				}
			}
		})
	case TaskUploadFile:
		// Single file
		info, sErr := os.Stat(task.Source)
		if sErr == nil {
			totalSize = info.Size()
		}
		jobs = []FileJob{{
			Path:     task.Name,
			AbsPath:  task.Source,
			DestPath: task.Dest,
			Size:     totalSize,
			IsDir:    false,
		}}
	case TaskDownloadFile:
		// Single remote file
		stat, sErr := q.client.sftpClient.Stat(task.Source)
		if sErr == nil {
			totalSize = stat.Size()
		}
		jobs = []FileJob{{
			Path:     task.Name,
			AbsPath:  task.Source,
			DestPath: task.Dest,
			Size:     totalSize,
			IsDir:    false,
		}}
	default:
		err = fmt.Errorf("unknown task type")
	}

	if err != nil {
		task.State = TaskFailed
		task.err = err
		q.notify(task)
		log.Printf("[ERROR] Task %d (%s) scanning failed: %v", task.ID, task.Name, err)
		return
	}

	if !useRsync {
		task.totalFiles = len(jobs)
		task.totalSize = totalSize
	}
	task.jobs = jobs

	// Transfer Phase
	task.State = TaskTransferring
	q.notify(task)
	log.Printf("[INFO] Task %d (%s) scanning done. Files: %d, Size: %d. Starting transfer.", task.ID, task.Name, task.totalFiles, task.totalSize)

	// Create Level 2 Queue (FileQueue)
	fq := NewFileQueue(128) // 64 concurrent files

	// Define executor based on task type
	executor := q.makeExecutor(task, useRsync, engine)

	// Monitor progress
	stopMonitor := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				q.notify(task)
			case <-stopMonitor:
				return
			case <-task.ctx.Done():
				return
			}
		}
	}()

	// Split jobs into directories and files to ensure dirs are created first
	var dirJobs []FileJob
	var fileJobs []FileJob

	for _, job := range jobs {
		if job.IsDir {
			dirJobs = append(dirJobs, job)
		} else {
			fileJobs = append(fileJobs, job)
		}
	}

	// Execute directory creation first
	if len(dirJobs) > 0 {
		log.Printf("[INFO] Creating %d directories...", len(dirJobs))
		q.updateChan <- TaskProgress{
			TaskID:  task.ID,
			State:   TaskTransferring,
			LastLog: fmt.Sprintf("Creating %d directories...", len(dirJobs)),
		}

		err = fq.ProcessJobs(task.ctx, dirJobs, executor, nil) // No progress update for dirs usually, or maybe?
		if err != nil {
			result = err
			return
		}
	}

	// Execute file transfers
	result = fq.ProcessJobs(task.ctx, fileJobs, executor, func(filesDone int, bytesDone int64) {
		atomic.StoreInt64(&task.Progress.BytesTransferred, bytesDone)
		task.Progress.CompletedFiles = filesDone + len(dirJobs) // Include dirs in count?
	})

	close(stopMonitor)
}

func (q *TaskQueue) makeExecutor(task *Task, useRsync bool, engine TransferEngine) JobExecutor {
	// return
	taskType := task.Type
	if taskType == TaskUploadDirectory || taskType == TaskUploadFile {
		executor := func(ctx context.Context, job FileJob) (int64, error) {
			if job.IsDir {
				// If using rsync, delegate to engine
				if useRsync {
					log.Printf("[INFO] Rsync Upload Directory: %s -> %s", job.AbsPath, job.DestPath)
					err := engine.UploadFile(ctx, job.AbsPath, job.DestPath, func(bytes int64, output string) error {
						// For rsync, bytes might be 0, output is the line
						if output != "" {
							q.updateChan <- TaskProgress{
								TaskID:  task.ID,
								State:   task.State,
								LastLog: output,
							}
						}
						return nil
					})
					if err != nil {
						log.Printf("[ERROR] Rsync Upload failed: %v", err)
					}
					return 0, err
				}
				// We rely on parent dirs being created or implicit creation for now
				if err := q.client.sftpClient.MkdirAll(job.DestPath); err != nil {
					log.Printf("[ERROR] Job Upload Mkdir failed: %s -> %v", job.DestPath, err)
					return 0, err
				}
				return 0, nil
			}
			err := engine.UploadFile(ctx, job.AbsPath, job.DestPath, nil)
			if err != nil {
				log.Printf("[ERROR] Job Upload failed: %s -> %s: %v", job.AbsPath, job.DestPath, err)
			}
			return job.Size, err
		}
		return executor
	}
	// Download
	executor := func(ctx context.Context, job FileJob) (int64, error) {
		if job.IsDir {
			if useRsync {
				log.Printf("[INFO] Rsync Download Directory: %s -> %s", job.AbsPath, job.DestPath)
				err := engine.DownloadFile(ctx, job.AbsPath, job.DestPath, func(bytes int64, output string) error {
					if output != "" {
						q.updateChan <- TaskProgress{
							TaskID:  task.ID,
							State:   task.State,
							LastLog: output,
						}
					}
					return nil
				})
				if err != nil {
					log.Printf("[ERROR] Rsync Download failed: %v", err)
				}
				return 0, err
			}
			if err := os.MkdirAll(job.DestPath, 0755); err != nil {
				log.Printf("[ERROR] Job Download Mkdir failed: %s -> %v", job.DestPath, err)
				return 0, err
			}
			return 0, nil
		}
		err := engine.DownloadFile(ctx, job.AbsPath, job.DestPath, nil)
		if err != nil {
			log.Printf("[ERROR] Job Download failed: %s -> %s: %v", job.AbsPath, job.DestPath, err)
		}
		return job.Size, err
	}
	return executor
}

func (q *TaskQueue) notify(task *Task) {
	// Build progress object
	prog := TaskProgress{
		TaskID:           task.ID,
		State:            task.State,
		TotalFiles:       task.totalFiles,
		CompletedFiles:   task.Progress.CompletedFiles,
		BytesTransferred: atomic.LoadInt64(&task.Progress.BytesTransferred),
		TotalSize:        task.totalSize,
		Percentage:       0,
	}

	if task.err != nil {
		prog.Error = task.err.Error()
	}

	if prog.TotalSize > 0 {
		prog.Percentage = int((float64(prog.BytesTransferred) / float64(prog.TotalSize)) * 100)
	}

	// Calculate speed
	now := time.Now()
	if !task.lastCheck.IsZero() {
		duration := now.Sub(task.lastCheck).Seconds()
		if duration >= 1.0 { // Update speed every second (approx)
			bytesDelta := prog.BytesTransferred - task.lastBytes
			if bytesDelta >= 0 {
				prog.CurrentSpeed = float64(bytesDelta) / duration
			}
			task.lastBytes = prog.BytesTransferred
			task.lastCheck = now
		} else {
			// Maintain previous speed until 1s passes
			prog.CurrentSpeed = task.Progress.CurrentSpeed
		}
	} else {
		task.lastCheck = now
		task.lastBytes = prog.BytesTransferred
	}

	// Update persistent state to current calculated speed
	task.Progress.CurrentSpeed = prog.CurrentSpeed

	select {
	case q.updateChan <- prog:
	default:
		// Drop update if channel full to prevent blocking
	}
}

func (q *TaskQueue) CancelAllTasks() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.State == TaskPending || t.State == TaskScanning || t.State == TaskTransferring {
			t.cancel()
			log.Printf("[INFO] Cancelling task %d via CancelAllTasks", t.ID)
		}
	}
}
