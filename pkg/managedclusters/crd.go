// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func getCustomResourceColumnDefinitions() []apiextensionsv1.CustomResourceColumnDefinition {
	return []apiextensionsv1.CustomResourceColumnDefinition{}
}
