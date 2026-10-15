package bot

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"sync"
)

var errSerializedQueueBackpressure = errors.New("serialized queue backpressure")
var errSerializedQueueClosed = errors.New("serialized queue closed")

type serializedExecutionQueue struct {
	mu      sync.Mutex
	workers []chan serializedQueueTask
	depth   int
	closed  bool
	wg      sync.WaitGroup
}

type serializedQueueTask struct {
	ctx  context.Context
	run  func(context.Context) error
	done chan error
}

func newSerializedExecutionQueue(workerCount, depth int) *serializedExecutionQueue {
	if workerCount <= 0 {
		workerCount = 1
	}
	if depth <= 0 {
		depth = 1
	}

	q := &serializedExecutionQueue{
		workers: make([]chan serializedQueueTask, workerCount),
		depth:   depth,
	}
	for i := 0; i < workerCount; i++ {
		ch := make(chan serializedQueueTask, depth)
		q.workers[i] = ch
		q.wg.Add(1)
		go q.runWorker(ch)
	}
	return q
}

func (q *serializedExecutionQueue) Submit(ctx context.Context, groupID int64, run func(context.Context) error) error {
	done, err := q.enqueue(ctx, groupID, run)
	if err != nil {
		return err
	}

	if ctx == nil {
		return <-done
	}
	select {
	case runErr := <-done:
		return runErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *serializedExecutionQueue) enqueue(ctx context.Context, groupID int64, run func(context.Context) error) (<-chan error, error) {
	if run == nil {
		return nil, errors.New("serialized queue task is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	task := serializedQueueTask{
		ctx:  ctx,
		run:  run,
		done: make(chan error, 1),
	}

	idx := q.partitionForGroup(groupID)

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil, errSerializedQueueClosed
	}
	ch := q.workers[idx]
	if len(ch) >= q.depth {
		return nil, errSerializedQueueBackpressure
	}
	select {
	case ch <- task:
		return task.done, nil
	default:
		return nil, errSerializedQueueBackpressure
	}
}

func (q *serializedExecutionQueue) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	for _, ch := range q.workers {
		close(ch)
	}
	q.mu.Unlock()

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *serializedExecutionQueue) usage() (queued, capacity int) {
	if q == nil {
		return 0, 0
	}

	q.mu.Lock()
	workers := append([]chan serializedQueueTask(nil), q.workers...)
	depth := q.depth
	q.mu.Unlock()

	if len(workers) == 0 || depth <= 0 {
		return 0, 0
	}
	capacity = len(workers) * depth
	for _, worker := range workers {
		queued += len(worker)
	}
	return queued, capacity
}

func (q *serializedExecutionQueue) partitionForGroup(groupID int64) int {
	if len(q.workers) == 1 {
		return 0
	}
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], uint64(groupID))
	hasher := fnv.New32a()
	_, _ = hasher.Write(raw[:])
	return int(hasher.Sum32() % uint32(len(q.workers)))
}

func (q *serializedExecutionQueue) runWorker(ch <-chan serializedQueueTask) {
	defer q.wg.Done()
	for task := range ch {
		if task.run == nil {
			task.done <- errors.New("serialized queue task is nil")
			continue
		}
		select {
		case <-task.ctx.Done():
			task.done <- task.ctx.Err()
			continue
		default:
		}
		task.done <- task.run(task.ctx)
	}
}
