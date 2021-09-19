// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

func filterByAuthorization(user string, groups []string, authorizationURL string) string {
	// to be used later
	_ = groups

	return "WHERE TRUE"
}
