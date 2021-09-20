// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4/pgxpool"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	"github.com/open-cluster-management/hub-of-hubs-nonk8s-api/pkg/authentication"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const syncIntervalInSeconds = 4

// ManagedClusters middleware.
func ManagedClusters(authorizationURL string, dbConnectionPool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := c.MustGet(authentication.UserKey).(string)
		if !ok {
			fmt.Fprintf(gin.DefaultWriter, "unable to get user from context")

			user = "Unknown"
		}

		groups, ok := c.MustGet(authentication.GroupsKey).([]string)
		if !ok {
			fmt.Fprintf(gin.DefaultWriter, "unable to get groups from context")

			groups = []string{}
		}

		fmt.Fprintf(gin.DefaultWriter, "got authenticated user: %v\n", user)
		fmt.Fprintf(gin.DefaultWriter, "user groups: %v\n", groups)

		query := sqlQuery(user, groups, authorizationURL)

		if _, watch := c.GetQuery("watch"); watch {
			handleRowsForWatch(c, query, dbConnectionPool)
			return
		}

		handleRows(c, query, dbConnectionPool)
	}
}

func sqlQuery(user string, groups []string, authorizationURL string) string {
	return "SELECT payload FROM status.managed_clusters " +
		filterByAuthorization(user, groups, authorizationURL, gin.DefaultWriter)
}

func handleRowsForWatch(ginCtx *gin.Context, query string, dbConnectionPool *pgxpool.Pool) {
	w := ginCtx.Writer
	header := w.Header()
	header.Set("Transfer-Encoding", "chunked")
	header.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(syncIntervalInSeconds * time.Second)

	ctx, cancelContext := context.WithCancel(context.Background())
	defer cancelContext()

	for {
		select {
		case <-w.CloseNotify():
			ticker.Stop()
			cancelContext()

			return
		case <-ticker.C:
			if ginCtx.Err() != nil || ginCtx.IsAborted() {
				ticker.Stop()
				cancelContext()

				return
			}

			doHandleRowsForWatch(ctx, w, query, dbConnectionPool)
		}
	}
}

func doHandleRowsForWatch(ctx context.Context, w io.Writer, query string, dbConnectionPool *pgxpool.Pool) {
	rows, err := dbConnectionPool.Query(ctx, query)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "error in quering managed clusters: %v\n", err)
	}

	for rows.Next() {
		managedCluster := &clusterv1.ManagedCluster{}

		err := rows.Scan(managedCluster)
		if err != nil {
			continue
		}

		watchEvent := &metav1.WatchEvent{Type: "ADDED", Object: runtime.RawExtension{Object: managedCluster}}

		json, err := json.Marshal(watchEvent)
		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "error in json marshalling: %v\n", err)
			continue
		}

		_, err = w.Write(json)

		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "error in writing response: %v\n", err)
			continue
		}

		_, err = w.Write([]byte("\n"))

		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "error in writing response: %v\n", err)
			continue
		}
	}

	w.(http.Flusher).Flush()
}

func handleRows(c *gin.Context, query string, dbConnectionPool *pgxpool.Pool) {
	rows, err := dbConnectionPool.Query(context.TODO(), query)
	if err != nil {
		c.String(http.StatusInternalServerError, "internal error")
		fmt.Fprintf(gin.DefaultWriter, "error in quering managed clusters: %v\n", err)
	}

	var managedClusters []*clusterv1.ManagedCluster

	for rows.Next() {
		managedCluster := &clusterv1.ManagedCluster{}

		err := rows.Scan(managedCluster)
		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "error in scanning a managed cluster: %v\n", err)
			continue
		}

		managedClusters = append(managedClusters, managedCluster)
	}

	c.JSON(http.StatusOK, managedClusters)
}
