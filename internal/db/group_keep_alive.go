package db

import (
	"database/sql"
	"errors"
	"time"
)

func (d *Database) initGroupKeepAlive(interval time.Duration, workers int, queueCap int) {
	if d == nil {
		return
	}
	if interval <= 0 {
		interval = defaultGroupKeepAliveInterval
	}
	if workers <= 0 {
		workers = defaultGroupKeepAliveWorkers
	}
	if queueCap <= 0 {
		queueCap = defaultGroupKeepAliveQueueCap
	}

	stop := make(chan struct{})
	queue := make(chan int64, queueCap)

	d.groupKeepAliveMu.Lock()
	d.groupKeepAliveInterval = interval
	d.groupKeepAliveStop = stop
	d.groupKeepAliveQueue = queue
	d.groupKeepAlivePending = make(map[int64]struct{})
	d.groupKeepAliveLastKick = make(map[int64]time.Time)
	d.groupKeepAliveMu.Unlock()

	for worker := 0; worker < workers; worker++ {
		d.groupKeepAliveWG.Add(1)
		go d.runGroupKeepAliveWorker(stop, queue)
	}
}

func (d *Database) stopGroupKeepAlive() {
	if d == nil {
		return
	}
	d.groupKeepAliveMu.Lock()
	stop := d.groupKeepAliveStop
	if stop == nil {
		d.groupKeepAliveMu.Unlock()
		return
	}
	d.groupKeepAliveStop = nil
	d.groupKeepAliveQueue = nil
	d.groupKeepAlivePending = nil
	d.groupKeepAliveLastKick = nil
	d.groupKeepAliveMu.Unlock()

	close(stop)
	d.groupKeepAliveWG.Wait()
}

func (d *Database) scheduleGroupKeepAlive(chatID int64) {
	if d == nil || chatID == 0 || d.contextCache == nil {
		return
	}
	now := time.Now().UTC()

	d.groupKeepAliveMu.Lock()
	stop := d.groupKeepAliveStop
	queue := d.groupKeepAliveQueue
	pending := d.groupKeepAlivePending
	lastKick := d.groupKeepAliveLastKick
	interval := d.groupKeepAliveInterval
	if stop == nil || queue == nil || pending == nil || lastKick == nil {
		d.groupKeepAliveMu.Unlock()
		return
	}
	if interval > 0 {
		if last, ok := lastKick[chatID]; ok && now.Sub(last) < interval {
			d.groupKeepAliveMu.Unlock()
			return
		}
	}
	if _, exists := pending[chatID]; exists {
		d.groupKeepAliveMu.Unlock()
		return
	}
	pending[chatID] = struct{}{}
	lastKick[chatID] = now
	d.groupKeepAliveMu.Unlock()

	select {
	case <-stop:
		d.clearGroupKeepAlivePending(chatID)
	case queue <- chatID:
	default:
		d.rollbackGroupKeepAlive(chatID)
	}
}

func (d *Database) runGroupKeepAliveWorker(stop <-chan struct{}, queue <-chan int64) {
	defer d.groupKeepAliveWG.Done()
	for {
		select {
		case <-stop:
			return
		case chatID := <-queue:
			d.refreshGroupKeepAlive(chatID)
		}
	}
}

func (d *Database) refreshGroupKeepAlive(chatID int64) {
	defer d.clearGroupKeepAlivePending(chatID)
	if d == nil || chatID == 0 || d.contextCache == nil {
		return
	}
	group, err := d.loadGroupConfigFromDB(chatID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			d.deleteCachedGroupConfig(chatID)
		}
		return
	}
	d.setCachedGroupConfig(group)
}

func (d *Database) clearGroupKeepAlivePending(chatID int64) {
	if d == nil || chatID == 0 {
		return
	}
	d.groupKeepAliveMu.Lock()
	if d.groupKeepAlivePending != nil {
		delete(d.groupKeepAlivePending, chatID)
	}
	d.groupKeepAliveMu.Unlock()
}

func (d *Database) rollbackGroupKeepAlive(chatID int64) {
	if d == nil || chatID == 0 {
		return
	}
	d.groupKeepAliveMu.Lock()
	if d.groupKeepAlivePending != nil {
		delete(d.groupKeepAlivePending, chatID)
	}
	if d.groupKeepAliveLastKick != nil {
		delete(d.groupKeepAliveLastKick, chatID)
	}
	d.groupKeepAliveMu.Unlock()
}
