// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/jackc/pgx/v4/pgxpool"
	"go.uber.org/zap"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

const environmentVariableDatabaseURL = "DATABASE_URL"

func printVersion(log logr.Logger) {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}

// function to handle defers with exit, see https://stackoverflow.com/a/27629493/553720.
func doMain() int {
	var log logr.Logger

	zapLog, err := zap.NewDevelopment()
	if err != nil {
		//nolint:forbidigo
		fmt.Printf("failed to create zap log: %v\n", err)
	}

	log = zapr.NewLogger(zapLog)

	printVersion(log)

	databaseURL, found := os.LookupEnv(environmentVariableDatabaseURL)
	if !found {
		log.Error(nil, "Not found:", "environment variable", environmentVariableDatabaseURL)
		return 1
	}

	dbConnectionPool, err := pgxpool.Connect(context.TODO(), databaseURL)
	if err != nil {
		log.Error(err, "Failed to connect to the database")
		return 1
	}
	defer dbConnectionPool.Close()

	return 0
}

func main() {
	os.Exit(doMain())
}
