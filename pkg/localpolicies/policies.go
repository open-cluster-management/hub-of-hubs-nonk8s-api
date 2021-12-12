// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package localpolicies

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4/pgxpool"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/hub-of-hubs-nonk8s-api/pkg/authentication"
)

// LocalPolicies middleware.
func LocalPolicies(authorizationURL string, authorizationCABundle []byte,
	dbConnectionPool *pgxpool.Pool) gin.HandlerFunc {
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

		handleRows(c, dbConnectionPool)
	}
}

func handleRows(c *gin.Context, dbConnectionPool *pgxpool.Pool) {
	rows, err := dbConnectionPool.Query(context.TODO(), "SELECT leaf_hub_name, payload from local_spec.policies")
	if err != nil {
		c.String(http.StatusInternalServerError, "internal error")
		fmt.Fprintf(gin.DefaultWriter, "error in quering managed clusters: %v\n", err)
	}

	policies := []*policiesv1.Policy{}

	for rows.Next() {
		policy := &policiesv1.Policy{}

		var leafHubName string

		err := rows.Scan(leafHubName, policy)
		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "error in scanning a policy: %v\n", err)
			continue
		}

		_ = leafHubName // handle leafHubName

		policies = append(policies, policy)
	}

	c.JSON(http.StatusOK, policies)
}
