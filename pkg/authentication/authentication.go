// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package authentication

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Authentication middleware.
func Authentication() gin.HandlerFunc {
	return func(c *gin.Context) {
		authorizationHeader := c.GetHeader("Authorization")
		if len(authorizationHeader) < 1 {
			c.Header("WWW-Authenticate", "")
			c.AbortWithStatus(http.StatusUnauthorized)

			return
		}

		c.Next()
	}
}
