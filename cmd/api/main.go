package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"cs2-stat/internal/server"
)

func main() {
	server := server.NewServer()

	done := make(chan bool, 1)
	go gracefulShutdown(server, done)

	log.Println("server running")
	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("http server error: %s", err))
	}

	<-done
	log.Println("Graceful shutdown complete.")
}

func gracefulShutdown(apiServer *http.Server, done chan bool) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()

	log.Println("shutting down gracefully, press Ctrl+C again to force")
	stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Clean up Chrome processes
	log.Println("Cleaning up Chrome processes...")
	exec.Command("pkill", "-f", "chrome").Run()
	exec.Command("pkill", "-f", "chromium").Run()

	if err := apiServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown with error: %v", err)
	}

	log.Println("Server exiting")

	done <- true
}
