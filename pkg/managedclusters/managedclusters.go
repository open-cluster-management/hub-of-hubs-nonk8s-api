// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4/pgxpool"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	"github.com/open-cluster-management/hub-of-hubs-nonk8s-api/pkg/authentication"
)

// ManagedClusters middleware.
func ManagedClusters(dbConnectionPool *pgxpool.Pool) gin.HandlerFunc {
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

		rows, err := dbConnectionPool.Query(context.TODO(), `SELECT payload FROM status.managed_clusters`)
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
}
