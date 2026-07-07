package orchestrator

import (
	"fmt"
	"log"
	"sync"
	"time"
	"github.com/docker/docker/client"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"database/sql"
)

type GlobalPauser interface {
	LockOthers(activeRunners []string)
	UnlockOthers()
}

type StatusReporter interface {
	MarkBusy(name string)
	MarkIdle(name string)
}

const (
	labelManaged = "github-mux.managed"
	labelRunner  = "github-mux.runner"

	namePrefixWarm   = "github-mux-warm-"
	namePrefixActive = "github-mux-active-"

	eventReplayMargin = 60 // seconds
)

type WarmWorker struct {
	ContainerID string
	IPAddress   string
}

type ActiveWorker struct {
	ContainerID string
	IPAddress   string
	RunnerName  string
}

type Orchestrator struct {
	pauser            GlobalPauser
	dockerCli         *client.Client
	db                *sql.DB
	queries           *sqlc.Queries
	mutex             sync.Mutex
	cond              *sync.Cond
	warmPool          map[string]*WarmWorker
	activeWorkers     map[string]*ActiveWorker
	activeListeners   map[string]int
	maxWorkers        int
	warmWorkersConfig int
	bootingCount      int
	isPaused          bool
	reporter          StatusReporter
}

func NewOrchestrator(pauser GlobalPauser, maxWorkers int, warmWorkers int, db *sql.DB, queries *sqlc.Queries) (*Orchestrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	o := &Orchestrator{
		pauser:            pauser,
		dockerCli:         cli,
		db:                db,
		queries:           queries,
		warmPool:          make(map[string]*WarmWorker),
		activeWorkers:     make(map[string]*ActiveWorker),
		activeListeners:   make(map[string]int),
		maxWorkers:        maxWorkers,
		warmWorkersConfig: warmWorkers,
		isPaused:          false,
	}
	o.cond = sync.NewCond(&o.mutex)

	since := fmt.Sprintf("%d", time.Now().Unix()-eventReplayMargin)

	if err := o.recoverState(); err != nil {
		log.Printf("[Orchestrator] Warning: state recovery failed (fresh start): %v", err)
	}

	go o.watchEvents(since)
	go o.maintainPool()

	return o, nil
}

func (o *Orchestrator) SetStatusReporter(reporter StatusReporter) {
	o.reporter = reporter
}

func (o *Orchestrator) logCapacityLocked() {
	total := len(o.warmPool) + len(o.activeWorkers) + o.bootingCount
	log.Printf("[Orchestrator] Capacity: %d warm, %d active, %d booting, %d/%d total",
		len(o.warmPool), len(o.activeWorkers), o.bootingCount, total, o.maxWorkers)
}

// UpdateSettings allows dynamic updating of capacity parameters.
func (o *Orchestrator) UpdateSettings(maxWorkers, warmWorkers int) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.maxWorkers = maxWorkers
	o.warmWorkersConfig = warmWorkers
	log.Printf("[Orchestrator] Settings updated: MaxWorkers=%d, WarmWorkers=%d", maxWorkers, warmWorkers)
	o.cond.Broadcast()
}

// GetStatus returns the current global capacity status
func (o *Orchestrator) GetStatus() map[string]interface{} {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return map[string]interface{}{
		"max_workers":  o.maxWorkers,
		"warm_workers": o.warmWorkersConfig,
		"warm_pool_size": len(o.warmPool),
		"active_workers": len(o.activeWorkers),
		"booting_count":  o.bootingCount,
		"is_paused":      o.isPaused,
	}
}
