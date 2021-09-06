// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/open-cluster-management/hub-of-hubs-nonk8s-api/pkg/authentication"
	"github.com/open-cluster-management/hub-of-hubs-nonk8s-api/pkg/managedclusters"
	"go.uber.org/zap"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

const (
	environmentVariableDatabaseURL   = "DATABASE_URL"
	environmentVariableClusterAPIURL = "CLUSTER_API_URL"
	secondsToFinishOnShutdown        = 5
)

func printVersion(log logr.Logger) {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}

func newLogger() logr.Logger {
	zapLog, err := zap.NewDevelopment()
	if err != nil {
		//nolint:forbidigo
		fmt.Printf("failed to create zap log: %v\n", err)
	}

	return zapr.NewLogger(zapLog)
}

// function to handle defers with exit, see https://stackoverflow.com/a/27629493/553720.
func doMain() int {
	log := newLogger()

	printVersion(log)

	databaseURL, found := os.LookupEnv(environmentVariableDatabaseURL)
	if !found {
		log.Error(nil, "Not found:", "environment variable", environmentVariableDatabaseURL)
		return 1
	}

	clusterAPIURL, found := os.LookupEnv(environmentVariableClusterAPIURL)
	if !found {
		log.Error(nil, "Not found:", "environment variable", environmentVariableClusterAPIURL)
		return 1
	}

	dbConnectionPool, err := pgxpool.Connect(context.TODO(), databaseURL)
	if err != nil {
		log.Error(err, "Failed to connect to the database")
		return 1
	}
	defer dbConnectionPool.Close()

	srv := createServer(clusterAPIURL, dbConnectionPool)

	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		if err := srv.ListenAndServe(); err != nil && errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "listenAndServe returned")
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	// kill (no param) default send syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be catch, so don't need add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down server")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), secondsToFinishOnShutdown*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error(err, "erver forced to shutdown")
	}

	log.Info("Server exiting")

	return 0
}

func createServer(clusterAPIURL string, dbConnectionPool *pgxpool.Pool) *http.Server {
	router := gin.Default()

	router.Use(authentication.Authentication(clusterAPIURL))
	router.GET("/managedclusters", managedclusters.ManagedClusters(dbConnectionPool))

	return &http.Server{
		Addr:    ":8080",
		Handler: router,
	}
}

func main() {
	os.Exit(doMain())
}
