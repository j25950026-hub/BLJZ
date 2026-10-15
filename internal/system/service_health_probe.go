package system

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	DefaultServiceHealthProbeHeartbeatTimeout = 30 * time.Second
	DefaultServiceHealthProbeScanInterval     = 5 * time.Second
)

type ServiceZombieAlert struct {
	DetectedAt       time.Time
	LastHeartbeatAt  time.Time
	LastHeartbeatSrc string
	StaleFor         time.Duration
	Timeout          time.Duration
	Reason           string
}

type ServiceHealthSnapshot struct {
	Live             bool
	Ready            bool
	LivenessReason   string
	ReadinessReason  string
	CheckedAt        time.Time
	LastHeartbeatAt  time.Time
	LastHeartbeatSrc string
	Timeout          time.Duration
}

type ServiceHealthProbeOptions struct {
	HeartbeatTimeout time.Duration
	ScanInterval     time.Duration
	Now              func() time.Time
	Logf             func(format string, args ...any)
	OnZombie         func(ServiceZombieAlert)
	ReadinessCheck   func() (bool, string)
}

type ServiceHealthProbe struct {
	heartbeatTimeout time.Duration
	scanInterval     time.Duration
	now              func() time.Time
	logf             func(format string, args ...any)
	onZombie         func(ServiceZombieAlert)
	readinessCheck   func() (bool, string)

	mu               sync.RWMutex
	ready            bool
	readyReason      string
	lastHeartbeatAt  time.Time
	lastHeartbeatSrc string
	forceUnhealthy   bool
	forceReason      string
	zombieAlerted    bool
}

func NewServiceHealthProbe(opts ServiceHealthProbeOptions) *ServiceHealthProbe {
	normalized := normalizeServiceHealthProbeOptions(opts)
	createdAt := normalized.Now()
	return &ServiceHealthProbe{
		heartbeatTimeout: normalized.HeartbeatTimeout,
		scanInterval:     normalized.ScanInterval,
		now:              normalized.Now,
		logf:             normalized.Logf,
		onZombie:         normalized.OnZombie,
		readinessCheck:   normalized.ReadinessCheck,
		ready:            false,
		readyReason:      "service is starting",
		lastHeartbeatAt:  createdAt,
		lastHeartbeatSrc: "bootstrap",
	}
}

func normalizeServiceHealthProbeOptions(opts ServiceHealthProbeOptions) ServiceHealthProbeOptions {
	normalized := opts
	if normalized.HeartbeatTimeout <= 0 {
		normalized.HeartbeatTimeout = DefaultServiceHealthProbeHeartbeatTimeout
	}
	if normalized.ScanInterval <= 0 {
		normalized.ScanInterval = DefaultServiceHealthProbeScanInterval
	}
	if normalized.Now == nil {
		normalized.Now = time.Now
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	return normalized
}

func (p *ServiceHealthProbe) Run(ctx context.Context) {
	if p == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(p.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.scanAndAlert()
		}
	}
}

func (p *ServiceHealthProbe) scanAndAlert() {
	if p == nil {
		return
	}
	now := p.now()

	p.mu.Lock()
	if p.forceUnhealthy || p.zombieAlerted {
		p.mu.Unlock()
		return
	}
	staleFor := now.Sub(p.lastHeartbeatAt)
	if staleFor <= p.heartbeatTimeout {
		p.mu.Unlock()
		return
	}

	reason := fmt.Sprintf(
		"heartbeat timeout: stale_for=%s timeout=%s",
		staleFor.Round(time.Millisecond),
		p.heartbeatTimeout,
	)
	alert := ServiceZombieAlert{
		DetectedAt:       now,
		LastHeartbeatAt:  p.lastHeartbeatAt,
		LastHeartbeatSrc: p.lastHeartbeatSrc,
		StaleFor:         staleFor,
		Timeout:          p.heartbeatTimeout,
		Reason:           reason,
	}
	p.forceUnhealthy = true
	p.forceReason = reason
	p.ready = false
	p.readyReason = "service became unready after liveness failure"
	p.zombieAlerted = true
	p.mu.Unlock()

	p.logf(
		"Liveness 心跳超时: last_heartbeat_at=%s source=%s stale_for=%s timeout=%s",
		alert.LastHeartbeatAt.Format(time.RFC3339),
		alert.LastHeartbeatSrc,
		alert.StaleFor.Round(time.Millisecond),
		alert.Timeout,
	)
	if p.onZombie != nil {
		p.onZombie(alert)
	}
}

func (p *ServiceHealthProbe) Heartbeat(source string) {
	if p == nil {
		return
	}
	normalizedSource := strings.TrimSpace(source)
	if normalizedSource == "" {
		normalizedSource = "unknown"
	}

	now := p.now()
	p.mu.Lock()
	p.lastHeartbeatAt = now
	p.lastHeartbeatSrc = normalizedSource
	p.mu.Unlock()
}

func (p *ServiceHealthProbe) MarkReady() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.ready = true
	p.readyReason = "ok"
	p.mu.Unlock()
}

func (p *ServiceHealthProbe) MarkNotReady(reason string) {
	if p == nil {
		return
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		normalizedReason = "service is not ready"
	}
	p.mu.Lock()
	p.ready = false
	p.readyReason = normalizedReason
	p.mu.Unlock()
}

func (p *ServiceHealthProbe) Snapshot() ServiceHealthSnapshot {
	if p == nil {
		now := time.Now()
		return ServiceHealthSnapshot{
			Live:            false,
			Ready:           false,
			LivenessReason:  "service health probe is nil",
			ReadinessReason: "service health probe is nil",
			CheckedAt:       now,
		}
	}
	now := p.now()
	p.mu.RLock()
	snapshot := p.snapshotLocked(now)
	p.mu.RUnlock()
	if snapshot.Ready && p.readinessCheck != nil {
		if ready, reason := p.readinessCheck(); !ready {
			snapshot.Ready = false
			snapshot.ReadinessReason = strings.TrimSpace(reason)
			if snapshot.ReadinessReason == "" {
				snapshot.ReadinessReason = "functional readiness check failed"
			}
		}
	}
	return snapshot
}

func (p *ServiceHealthProbe) LivenessOK() (bool, string) {
	snapshot := p.Snapshot()
	return snapshot.Live, snapshot.LivenessReason
}

func (p *ServiceHealthProbe) ReadinessOK() (bool, string) {
	snapshot := p.Snapshot()
	return snapshot.Ready, snapshot.ReadinessReason
}

func (p *ServiceHealthProbe) snapshotLocked(now time.Time) ServiceHealthSnapshot {
	live := true
	livenessReason := "ok"
	staleFor := now.Sub(p.lastHeartbeatAt)
	if staleFor > p.heartbeatTimeout {
		live = false
		livenessReason = fmt.Sprintf(
			"heartbeat timeout: stale_for=%s timeout=%s",
			staleFor.Round(time.Millisecond),
			p.heartbeatTimeout,
		)
	}
	if p.forceUnhealthy {
		live = false
		livenessReason = p.forceReason
	}

	ready := p.ready && live
	readinessReason := p.readyReason
	if strings.TrimSpace(readinessReason) == "" {
		readinessReason = "ok"
	}
	if !p.ready {
		if strings.TrimSpace(p.readyReason) == "" {
			readinessReason = "service is not ready"
		}
	} else if !live {
		readinessReason = livenessReason
	}

	return ServiceHealthSnapshot{
		Live:             live,
		Ready:            ready,
		LivenessReason:   livenessReason,
		ReadinessReason:  readinessReason,
		CheckedAt:        now,
		LastHeartbeatAt:  p.lastHeartbeatAt,
		LastHeartbeatSrc: p.lastHeartbeatSrc,
		Timeout:          p.heartbeatTimeout,
	}
}

type ServiceHealthHTTPServerOptions struct {
	Addr           string
	Probe          *ServiceHealthProbe
	LivenessPath   string
	ReadinessPath  string
	MetricsPath    string
	MetricsHandler http.Handler
	Logf           func(format string, args ...any)
}

type ServiceHealthHTTPServer struct {
	addr           string
	probe          *ServiceHealthProbe
	livenessPath   string
	readinessPath  string
	metricsPath    string
	metricsHandler http.Handler
	logf           func(format string, args ...any)
}

func NewServiceHealthHTTPServer(opts ServiceHealthHTTPServerOptions) (*ServiceHealthHTTPServer, error) {
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		return nil, fmt.Errorf("probe server addr must not be empty")
	}
	if opts.Probe == nil {
		return nil, fmt.Errorf("probe server probe must not be nil")
	}
	livenessPath := strings.TrimSpace(opts.LivenessPath)
	if livenessPath == "" {
		livenessPath = DefaultK8sProbeLivenessPath
	}
	readinessPath := strings.TrimSpace(opts.ReadinessPath)
	if readinessPath == "" {
		readinessPath = DefaultK8sProbeReadinessPath
	}
	metricsPath := strings.TrimSpace(opts.MetricsPath)
	metricsHandler := opts.MetricsHandler
	if metricsHandler != nil {
		if metricsPath == "" {
			metricsPath = DefaultPrometheusMetricsPath
		}
		metricsPath = ensureHTTPPath(metricsPath)
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &ServiceHealthHTTPServer{
		addr:           addr,
		probe:          opts.Probe,
		livenessPath:   ensureHTTPPath(livenessPath),
		readinessPath:  ensureHTTPPath(readinessPath),
		metricsPath:    metricsPath,
		metricsHandler: metricsHandler,
		logf:           logf,
	}, nil
}

func (s *ServiceHealthHTTPServer) Addr() string {
	if s == nil {
		return ""
	}
	return s.addr
}

func (s *ServiceHealthHTTPServer) Handler() http.Handler {
	if s == nil || s.probe == nil {
		return http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	mux.HandleFunc(s.livenessPath, func(w http.ResponseWriter, _ *http.Request) {
		s.respondProbeStatus(w, "liveness")
	})
	mux.HandleFunc(s.readinessPath, func(w http.ResponseWriter, _ *http.Request) {
		s.respondProbeStatus(w, "readiness")
	})
	if s.metricsHandler != nil {
		mux.Handle(s.metricsPath, s.metricsHandler)
	}
	return mux
}

func (s *ServiceHealthHTTPServer) Run(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	server := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	endpoints := []string{s.livenessPath, s.readinessPath}
	if s.metricsHandler != nil {
		endpoints = append(endpoints, s.metricsPath)
	}
	s.logf("Liveness/Readiness 探针服务已启动：addr=%s endpoints=%s", s.addr, strings.Join(endpoints, ","))
	if err := server.ListenAndServe(); err != nil {
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
	return nil
}

func (s *ServiceHealthHTTPServer) respondProbeStatus(w http.ResponseWriter, kind string) {
	snapshot := s.probe.Snapshot()
	ok := false
	reason := ""
	switch kind {
	case "liveness":
		ok = snapshot.Live
		reason = snapshot.LivenessReason
	case "readiness":
		ok = snapshot.Ready
		reason = snapshot.ReadinessReason
	default:
		ok = false
		reason = "unknown probe kind"
	}

	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	payload := map[string]any{
		"ok":                 ok,
		"probe":              kind,
		"reason":             reason,
		"checked_at":         snapshot.CheckedAt.UTC().Format(time.RFC3339Nano),
		"last_heartbeat_at":  snapshot.LastHeartbeatAt.UTC().Format(time.RFC3339Nano),
		"last_heartbeat_src": snapshot.LastHeartbeatSrc,
	}
	writeJSON(w, status, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "marshal json failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
