package scheduler

import (
	"log"
	"time"

	"batteryops/internal/service"
)

// Scheduler runs periodic background checks for overdue transfers, dispatch
// SLA breaches, and inventory replenishment.
type Scheduler struct {
	service  *service.Service
	interval time.Duration
	stopCh   chan struct{}
	done     chan struct{}
}

// New creates a new Scheduler that ticks at the given interval.
func New(svc *service.Service, interval time.Duration) *Scheduler {
	return &Scheduler{
		service:  svc,
		interval: interval,
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the scheduler goroutine.
func (s *Scheduler) Start() {
	go s.run()
}

// Stop signals the scheduler to stop and waits for it to drain.
func (s *Scheduler) Stop() {
	close(s.stopCh)
	<-s.done
}

func (s *Scheduler) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick performs one cycle of background checks.
func (s *Scheduler) tick() {
	transferred := s.service.CheckOverdueTransfers()
	for _, o := range transferred {
		log.Printf("scheduler: auto-transferred maintenance order %s to backup engineer %s", o.ID, o.BackupEngineerID)
	}

	reqs := s.service.CheckInventory()
	for _, r := range reqs {
		log.Printf("scheduler: created replenishment request %s for part %s", r.ID, r.PartID)
	}

	overdue := s.service.CheckOverdueDispatches()
	for _, o := range overdue {
		log.Printf("scheduler: dispatch SLA breached for maintenance order %s (reported %v ago)",
			o.ID, time.Since(o.ReportedAt).Round(time.Second))
	}
}
