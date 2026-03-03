package temporalworker

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// StartWorker creates and starts a worker for the pathwalk task queue.
// It registers PathwayWorkflow and the provided PathwayActivities.
func StartWorker(c client.Client, acts *PathwayActivities) (worker.Worker, error) {
	w := worker.New(c, TaskQueue, worker.Options{})

	w.RegisterWorkflow(PathwayWorkflow)
	w.RegisterActivity(acts.ExecuteStep)

	return w, nil
}
