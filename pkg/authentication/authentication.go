// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package authentication

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Authentication middleware.
func Authentication(clusterAPIURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authorizationHeader := c.GetHeader("Authorization")
		if !ok(authorizationHeader, clusterAPIURL) {
			c.Header("WWW-Authenticate", "")
			c.AbortWithStatus(http.StatusUnauthorized)

			return
		}

		c.Next()
	}
}

func ok(authorizationHeader string, clusterAPIURL string) bool {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	req, err := http.NewRequestWithContext(context.TODO(), "GET", fmt.Sprintf("%s/apis/user.openshift.io/v1/users/~",
		clusterAPIURL), nil)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "unable to create request: %v\n", err)
	}

	req.Header.Add("Authorization", authorizationHeader)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "got authentication error: %v\n", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "unable to read authentication response body: %v\n", err)
		return false
	}

	fmt.Fprintf(gin.DefaultWriter, "got authenticated user: %v\n", string(body))

	return true
}
