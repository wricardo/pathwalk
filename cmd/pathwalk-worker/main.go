package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wricardo/pathwalk/temporalworker"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	// Get Temporal configuration from environment or use defaults.
	hostPort := os.Getenv("TEMPORAL_HOST")
	if hostPort == "" {
		hostPort = "localhost:7233"
	}

	namespace := os.Getenv("TEMPORAL_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	// Connect to Temporal.
	c, err := client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if err != nil {
		log.Fatalf("Failed to connect to Temporal: %v", err)
	}
	defer c.Close()

	fmt.Printf("Connected to Temporal at %s (namespace: %s)\n", hostPort, namespace)

	// Start the worker.
	w, err := temporalworker.StartWorker(c, &temporalworker.PathwayActivities{})
	if err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	fmt.Printf("Worker started on task queue: %s\n", temporalworker.TaskQueue)

	// Run the worker in a goroutine.
	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			log.Fatalf("Worker error: %v", err)
		}
	}()

	// Wait for a signal to gracefully shut down.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("Shutting down worker...")
	w.Stop()
}
