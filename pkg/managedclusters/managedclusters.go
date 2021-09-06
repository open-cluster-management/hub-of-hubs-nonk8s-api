// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4/pgxpool"
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

		c.String(http.StatusOK, fmt.Sprintf("Managed Clusters for user %s, groups = %v\n", user, groups))
	}
}
