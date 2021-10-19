// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
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
	environmentVariableDatabaseURL               = "DATABASE_URL"
	environmentVariableClusterAPIURL             = "CLUSTER_API_URL"
	environmentVariableClusterAPICABundlePath    = "CLUSTER_API_CA_BUNDLE_PATH"
	environmentVariableAuthorizationURL          = "AUTHORIZATION_URL"
	environmentVariableAuthorizationCABundlePath = "AUTHORIZATION_CA_BUNDLE_PATH"
	environmentVariableKeyPath                   = "KEY_PATH"
	environmentVariableCertificatePath           = "CERTIFICATE_PATH"
	environmentVariableBasePath                  = "BASE_PATH"
	secondsToFinishOnShutdown                    = 5
)

var (
	errEnvironmentVariableNotFound = errors.New("not found environment variable")
	errFailedToLoadCertificate     = errors.New("failed to load certificate/key")
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

func readEnvironmentVariables() (string, string, string, string, string, string, string, string, error) {
	databaseURL, found := os.LookupEnv(environmentVariableDatabaseURL)
	if !found {
		return "", "", "", "", "", "", "", "", fmt.Errorf("%w: %s",
			errEnvironmentVariableNotFound, environmentVariableDatabaseURL)
	}

	clusterAPIURL, found := os.LookupEnv(environmentVariableClusterAPIURL)
	if !found {
		return "", "", "", "", "", "", "", "",
			fmt.Errorf("%w: %s", errEnvironmentVariableNotFound, environmentVariableClusterAPIURL)
	}

	authorizationURL, found := os.LookupEnv(environmentVariableAuthorizationURL)
	if !found {
		return "", "", "", "", "", "", "", "",
			fmt.Errorf("%w: %s", errEnvironmentVariableNotFound, environmentVariableAuthorizationURL)
	}

	clusterAPICABundlePath, found := os.LookupEnv(environmentVariableClusterAPICABundlePath)
	if !found {
		clusterAPICABundlePath = ""
	}

	authorizationCABundlePath, found := os.LookupEnv(environmentVariableAuthorizationCABundlePath)
	if !found {
		authorizationCABundlePath = ""
	}

	keyPath, found := os.LookupEnv(environmentVariableKeyPath)
	if !found {
		return "", "", "", "", "", "", "", "",
			fmt.Errorf("%w: %s", errEnvironmentVariableNotFound, environmentVariableKeyPath)
	}

	certificatePath, found := os.LookupEnv(environmentVariableCertificatePath)
	if !found {
		return "", "", "", "", "", "", "", "",
			fmt.Errorf("%w: %s", errEnvironmentVariableNotFound, environmentVariableCertificatePath)
	}

	basePath, found := os.LookupEnv(environmentVariableBasePath)
	if !found {
		basePath = ""
	}

	return databaseURL, clusterAPIURL, clusterAPICABundlePath, authorizationURL, authorizationCABundlePath, keyPath,
		certificatePath, basePath, nil
}

func readCertificates(clusterAPICABundlePath, authorizationCABundlePath, certificatePath,
	keyPath string) ([]byte, []byte, tls.Certificate, error) {
	var (
		clusterAPICABundle    []byte
		authorizationCABundle []byte
		certificate           tls.Certificate
	)

	if clusterAPICABundlePath != "" {
		clusterAPICABundle, err := ioutil.ReadFile(clusterAPICABundlePath)
		if err != nil {
			return clusterAPICABundle, authorizationCABundle, certificate,
				fmt.Errorf("%w: %s", errFailedToLoadCertificate, clusterAPICABundlePath)
		}
	}

	if authorizationCABundlePath != "" {
		authorizationCABundle, err := ioutil.ReadFile(authorizationCABundlePath)
		if err != nil {
			return clusterAPICABundle, authorizationCABundle, certificate,
				fmt.Errorf("%w: %s", errFailedToLoadCertificate, authorizationCABundle)
		}
	}

	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		return clusterAPICABundle, authorizationCABundle, certificate,
			fmt.Errorf("%w: %s/%s", errFailedToLoadCertificate, certificatePath, keyPath)
	}

	return clusterAPICABundle, authorizationCABundle, certificate, nil
}

// function to handle defers with exit, see https://stackoverflow.com/a/27629493/553720.
func doMain() int {
	log := newLogger()
	printVersion(log)

	databaseURL, clusterAPIURL, clusterAPICABundlePath, authorizationURL, authorizationCABundlePath, keyPath,
		certificatePath, basePath, err := readEnvironmentVariables()
	if err != nil {
		log.Error(err, "Failed to read environment variables")
		return 1
	}

	dbConnectionPool, err := pgxpool.Connect(context.TODO(), databaseURL)
	if err != nil {
		log.Error(err, "Failed to connect to the database")
		return 1
	}
	defer dbConnectionPool.Close()

	clusterAPICABundle, authorizationCABundle, _, err :=
		readCertificates(clusterAPICABundlePath, authorizationCABundlePath, certificatePath, keyPath)
	if err != nil {
		log.Error(err, "Failed to read certificates")
		return 1
	}

	srv := createServer(clusterAPIURL, clusterAPICABundle, authorizationURL,
		authorizationCABundle, dbConnectionPool, basePath)

	// Initializing the server in a goroutine so that it won't block the graceful shutdown handling below
	go func() {
		if err := srv.ListenAndServeTLS(certificatePath, keyPath); err != nil && errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "listenAndServe returned")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down server")

	// The context is used to inform the server it has 5 seconds to finish the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), secondsToFinishOnShutdown*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error(err, "erver forced to shutdown")
	}

	log.Info("Server exiting")

	return 0
}

func createServer(clusterAPIURL string, clusterAPICABundle []byte, authorizationURL string,
	authorizationCABundle []byte, dbConnectionPool *pgxpool.Pool, basePath string) *http.Server {
	router := gin.Default()

	router.Use(authentication.Authentication(clusterAPIURL, clusterAPICABundle))

	routerGroup := router.Group(basePath)
	routerGroup.GET("/managedclusters", managedclusters.ManagedClusters(authorizationURL,
		authorizationCABundle, dbConnectionPool))

	return &http.Server{
		Addr:    ":8080",
		Handler: router,
	}
}

func main() {
	os.Exit(doMain())
}
