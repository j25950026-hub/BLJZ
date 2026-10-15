package bot

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"time"
)

var errPriorityQueueBackpressure = errors.New("priority queue backpressure")
var errPriorityQueueClosed = errors.New("priority queue closed")
var errOutboundDropped = errors.New("outbound message dropped")

type outboundMessagePriority int

const (
	outboundPriorityBookkeepingReceipt outboundMessagePriority = iota
	outboundPriorityErrorAlert
	outboundPriorityNormalChat
	outboundPriorityBroadcastAd
)

type priorityDispatchQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tasks   priorityTaskHeap
	depth   int
	seq     uint64
	closed  bool
	workers int
	metrics OutboundMetricsRecorder
	wg      sync.WaitGroup
}

type priorityTask struct {
	priority   outboundMessagePriority
	seq        uint64
	ctx        context.Context
	enqueuedAt time.Time
	run        func(context.Context) error
	done       chan error
}

type priorityTaskHeap []priorityTask

func (h priorityTaskHeap) Len() int {
	return len(h)
}

func (h priorityTaskHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority < h[j].priority
	}
	return h[i].seq < h[j].seq
}

func (h priorityTaskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *priorityTaskHeap) Push(x any) {
	*h = append(*h, x.(priorityTask))
}

func (h *priorityTaskHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func newPriorityDispatchQueue(depth int) *priorityDispatchQueue {
	return newPriorityDispatchQueueWithWorkers(depth, 1)
}

func newPriorityDispatchQueueWithWorkers(depth, workers int) *priorityDispatchQueue {
	if depth <= 0 {
		depth = 1
	}
	if workers <= 0 {
		workers = 1
	}

	q := &priorityDispatchQueue{
		depth:   depth,
		tasks:   make(priorityTaskHeap, 0, depth),
		workers: workers,
		metrics: noopOutboundMetricsRecorder{},
	}
	q.cond = sync.NewCond(&q.mu)
	heap.Init(&q.tasks)
	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.runWorker()
	}
	return q
}

func (q *priorityDispatchQueue) Submit(ctx context.Context, priority outboundMessagePriority, run func(context.Context) error) error {
	done, err := q.enqueue(ctx, priority, run)
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

func (q *priorityDispatchQueue) enqueue(ctx context.Context, priority outboundMessagePriority, run func(context.Context) error) (<-chan error, error) {
	if run == nil {
		return nil, errors.New("priority queue task is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	task := priorityTask{
		priority:   normalizeOutboundPriority(priority),
		ctx:        ctx,
		enqueuedAt: time.Now(),
		run:        run,
		done:       make(chan error, 1),
	}

	q.mu.Lock()

	if q.closed {
		q.mu.Unlock()
		q.recordSubmission(task.priority, outboundMetricResultRejected)
		return nil, errPriorityQueueClosed
	}
	if len(q.tasks) >= q.depth {
		q.mu.Unlock()
		q.recordSubmission(task.priority, outboundMetricResultRejected)
		return nil, errPriorityQueueBackpressure
	}

	task.seq = q.seq
	q.seq++
	heap.Push(&q.tasks, task)
	q.cond.Signal()
	queued, capacity, workers, metrics := q.metricsSnapshotLocked()
	q.mu.Unlock()

	metrics.ObserveOutboundQueue(queued, capacity, workers)
	metrics.ObserveOutboundQueueSubmission(outboundPriorityMetricLabel(task.priority), outboundMetricResultAccepted)
	return task.done, nil
}

func (q *priorityDispatchQueue) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	q.cond.Broadcast()
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

func (q *priorityDispatchQueue) usage() (queued, capacity int) {
	if q == nil {
		return 0, 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks), q.depth
}

func (q *priorityDispatchQueue) SetMetrics(recorder OutboundMetricsRecorder) {
	if q == nil {
		return
	}
	if recorder == nil {
		recorder = noopOutboundMetricsRecorder{}
	}
	q.mu.Lock()
	q.metrics = recorder
	queued, capacity, workers, metrics := q.metricsSnapshotLocked()
	q.mu.Unlock()
	metrics.ObserveOutboundQueue(queued, capacity, workers)
}

func (q *priorityDispatchQueue) workerCount() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.workers
}

func (q *priorityDispatchQueue) runWorker() {
	defer q.wg.Done()
	for {
		task, ok := q.dequeue()
		if !ok {
			return
		}
		if task.run == nil {
			task.done <- errors.New("priority queue task is nil")
			continue
		}
		select {
		case <-task.ctx.Done():
			task.done <- task.ctx.Err()
			continue
		default:
		}
		metrics := q.currentMetrics()
		priorityLabel := outboundPriorityMetricLabel(task.priority)
		if !task.enqueuedAt.IsZero() {
			metrics.ObserveOutboundQueueWait(priorityLabel, time.Since(task.enqueuedAt))
		}
		startedAt := time.Now()
		err := task.run(task.ctx)
		result := outboundMetricResultSuccess
		if err != nil {
			if errorsIsContextDone(err) {
				result = outboundMetricResultCanceled
			} else {
				result = outboundMetricResultError
			}
		}
		metrics.ObserveOutboundTask(priorityLabel, result, time.Since(startedAt))
		task.done <- err
	}
}

func (q *priorityDispatchQueue) dequeue() (priorityTask, bool) {
	q.mu.Lock()

	for len(q.tasks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.tasks) == 0 {
		q.mu.Unlock()
		return priorityTask{}, false
	}
	task, _ := heap.Pop(&q.tasks).(priorityTask)
	queued, capacity, workers, metrics := q.metricsSnapshotLocked()
	q.mu.Unlock()
	metrics.ObserveOutboundQueue(queued, capacity, workers)
	return task, true
}

func (q *priorityDispatchQueue) recordSubmission(priority outboundMessagePriority, result string) {
	if q == nil {
		return
	}
	q.currentMetrics().ObserveOutboundQueueSubmission(outboundPriorityMetricLabel(priority), result)
}

func (q *priorityDispatchQueue) currentMetrics() OutboundMetricsRecorder {
	if q == nil {
		return noopOutboundMetricsRecorder{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.metrics == nil {
		return noopOutboundMetricsRecorder{}
	}
	return q.metrics
}

func (q *priorityDispatchQueue) metricsSnapshotLocked() (queued, capacity, workers int, metrics OutboundMetricsRecorder) {
	if q == nil {
		return 0, 0, 0, noopOutboundMetricsRecorder{}
	}
	metrics = q.metrics
	if metrics == nil {
		metrics = noopOutboundMetricsRecorder{}
	}
	return len(q.tasks), q.depth, q.workers, metrics
}

func normalizeOutboundPriority(priority outboundMessagePriority) outboundMessagePriority {
	switch priority {
	case outboundPriorityBookkeepingReceipt,
		outboundPriorityErrorAlert,
		outboundPriorityNormalChat,
		outboundPriorityBroadcastAd:
		return priority
	default:
		return outboundPriorityNormalChat
	}
}
