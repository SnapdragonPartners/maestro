package dispatch

import (
	"context"
)

// goroutineStrategy runs the dispatcher with background goroutines (production mode).
type goroutineStrategy struct{}

func (g *goroutineStrategy) Run(d *Dispatcher, ctx context.Context) error {
	// Use the dispatcher's WaitGroup, as the methods already call d.wg.Done()
	d.wg.Add(3)
	go d.messageProcessor(ctx)
	go d.supervisor(ctx)
	go d.metricsMonitor(ctx)
	return nil
}

func (g *goroutineStrategy) Stop() error {
	// No need to wait here as the Stop method handles the WaitGroup
	return nil
}
