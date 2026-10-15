package bot

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlPoint125SerializedExecutionQueue(t *testing.T) {
	t.Run("控制点 1.2.5 功能验证", func(t *testing.T) {
		queue := newSerializedExecutionQueue(4, 1000)
		defer closeQueueForTest(t, queue)

		groupID := int64(-100001)
		otherGroupID := findGroupOnDifferentPartition(t, queue, groupID)

		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		secondStarted := make(chan struct{})
		otherStarted := make(chan struct{})

		firstErrCh := make(chan error, 1)
		go func() {
			firstErrCh <- queue.Submit(context.Background(), groupID, func(context.Context) error {
				close(firstStarted)
				<-releaseFirst
				return nil
			})
		}()

		waitSignal(t, firstStarted, "首个同群任务未启动")

		secondErrCh := make(chan error, 1)
		go func() {
			secondErrCh <- queue.Submit(context.Background(), groupID, func(context.Context) error {
				close(secondStarted)
				return nil
			})
		}()

		ensureNoSignal(t, secondStarted, 80*time.Millisecond, "同一群组任务应串行执行")

		otherErrCh := make(chan error, 1)
		go func() {
			otherErrCh <- queue.Submit(context.Background(), otherGroupID, func(context.Context) error {
				close(otherStarted)
				return nil
			})
		}()

		waitSignal(t, otherStarted, "不同分区群组任务应可并行启动")
		if err := <-otherErrCh; err != nil {
			t.Fatalf("不同群组任务执行失败: %v", err)
		}

		close(releaseFirst)
		waitSignal(t, secondStarted, "首任务完成后同群后续任务未启动")
		if err := <-firstErrCh; err != nil {
			t.Fatalf("首个同群任务执行失败: %v", err)
		}
		if err := <-secondErrCh; err != nil {
			t.Fatalf("后续同群任务执行失败: %v", err)
		}
	})

	t.Run("队列背压验证", func(t *testing.T) {
		queue := newSerializedExecutionQueue(1, 1000)
		defer closeQueueForTest(t, queue)

		started := make(chan struct{})
		release := make(chan struct{})
		blockedDone, err := queue.enqueue(context.Background(), 2001, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
		if err != nil {
			t.Fatalf("准备阻塞任务失败: %v", err)
		}
		waitSignal(t, started, "阻塞任务未启动")

		doneChans := make([]<-chan error, 0, 1000)
		for i := 0; i < 1000; i++ {
			done, enqueueErr := queue.enqueue(context.Background(), 2001, func(context.Context) error {
				return nil
			})
			if enqueueErr != nil {
				t.Fatalf("填充队列失败(index=%d): %v", i, enqueueErr)
			}
			doneChans = append(doneChans, done)
		}

		if _, enqueueErr := queue.enqueue(context.Background(), 2001, func(context.Context) error {
			return nil
		}); !errors.Is(enqueueErr, errSerializedQueueBackpressure) {
			t.Fatalf("队列深度超过 1000 时应触发背压，实际=%v", enqueueErr)
		}

		close(release)
		waitTaskDone(t, blockedDone, "阻塞任务")
		for idx, done := range doneChans {
			waitTaskDone(t, done, "排队任务#"+strconv.Itoa(idx))
		}
	})

	t.Run("优雅关闭等待消费验证", func(t *testing.T) {
		queue := newSerializedExecutionQueue(1, 8)

		started := make(chan struct{})
		release := make(chan struct{})
		firstDone, err := queue.enqueue(context.Background(), 3001, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
		if err != nil {
			t.Fatalf("准备首任务失败: %v", err)
		}

		var secondExecuted int32
		secondDone, err := queue.enqueue(context.Background(), 3001, func(context.Context) error {
			atomic.StoreInt32(&secondExecuted, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("准备尾任务失败: %v", err)
		}

		waitSignal(t, started, "首任务未启动")

		closeDone := make(chan struct{})
		var closeErr error
		go func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			closeErr = queue.Close(closeCtx)
			close(closeDone)
		}()

		ensureNoSignal(t, closeDone, 80*time.Millisecond, "关闭过程应等待队列消费完成")
		close(release)

		select {
		case <-closeDone:
			if closeErr != nil {
				t.Fatalf("优雅关闭失败: %v", closeErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("优雅关闭超时")
		}

		waitTaskDone(t, firstDone, "首任务")
		waitTaskDone(t, secondDone, "尾任务")
		if atomic.LoadInt32(&secondExecuted) != 1 {
			t.Fatalf("关闭前应完成已入队任务消费")
		}

		if err := queue.Submit(context.Background(), 3001, func(context.Context) error { return nil }); !errors.Is(err, errSerializedQueueClosed) {
			t.Fatalf("关闭后应拒绝新任务，实际=%v", err)
		}
	})
}

func closeQueueForTest(t *testing.T, queue *serializedExecutionQueue) {
	t.Helper()
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := queue.Close(closeCtx); err != nil {
		t.Fatalf("关闭队列失败: %v", err)
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, failMsg string) {
	t.Helper()
	select {
	case <-ch:
		return
	case <-time.After(time.Second):
		t.Fatalf("%s", failMsg)
	}
}

func ensureNoSignal(t *testing.T, ch <-chan struct{}, wait time.Duration, failMsg string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s", failMsg)
	case <-time.After(wait):
	}
}

func waitTaskDone(t *testing.T, done <-chan error, taskName string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s执行失败: %v", taskName, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s等待结果超时", taskName)
	}
}

func findGroupOnDifferentPartition(t *testing.T, queue *serializedExecutionQueue, groupID int64) int64 {
	t.Helper()
	base := queue.partitionForGroup(groupID)
	for candidate := groupID + 1; candidate < groupID+10000; candidate++ {
		if queue.partitionForGroup(candidate) != base {
			return candidate
		}
	}
	t.Fatalf("无法找到不同分区的群组ID")
	return 0
}
