package logger

import (
	"errors"
	"sync"
)

// One worker and a bounded queue keep CPU, goroutine and pending-name costs
// bounded. A full queue applies backpressure to the rotating caller.
const maintenanceQueueSize = 16

type maintenanceJob struct {
	archive string
	done    chan struct{}
}

type fileMaintenance struct {
	jobs        chan maintenanceJob
	done        chan struct{}
	mu          sync.Mutex
	pending     map[string]bool
	errorMu     sync.Mutex
	firstError  error
	reportError error
}

func (w *fileWriter) startMaintenance(countError func(error)) {
	m := &fileMaintenance{jobs: make(chan maintenanceJob, maintenanceQueueSize), done: make(chan struct{}), pending: make(map[string]bool)}
	w.maintenance = m
	go func() {
		defer close(m.done)
		for job := range m.jobs {
			if job.done != nil {
				close(job.done)
				continue
			}
			archive := job.archive
			var err error
			if archive != "" && w.compress {
				err = w.compressFn(archive)
			}
			m.mu.Lock()
			delete(m.pending, archive)
			err = errors.Join(err, w.maintain())
			m.mu.Unlock()
			if err != nil {
				countError(err)
				m.errorMu.Lock()
				if m.firstError == nil {
					m.firstError = err
				}
				m.reportError = err
				m.errorMu.Unlock()
			}
		}
	}()
}

func (w *fileWriter) scheduleMaintenance(archive string) error {
	if w.maintenance == nil {
		if archive == "" {
			return w.maintain()
		}
		return w.finishRotation(archive)
	}
	w.maintenance.jobs <- maintenanceJob{archive: archive}
	return nil
}

func (w *fileWriter) pendingMaintenanceError() error {
	if w.maintenance == nil {
		return nil
	}
	m := w.maintenance
	m.errorMu.Lock()
	defer m.errorMu.Unlock()
	err := m.reportError
	m.reportError = nil
	return err
}

func (w *fileWriter) drainMaintenance() error {
	if w.maintenance == nil {
		return nil
	}
	m := w.maintenance
	close(m.jobs)
	<-m.done
	m.errorMu.Lock()
	defer m.errorMu.Unlock()
	return m.firstError
}
