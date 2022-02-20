[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Hub-of-Hubs Nonk8s API

[![Go Report Card](https://goreportcard.com/badge/github.com/stolostron/hub-of-hubs-spec-sync)](https://goreportcard.com/report/github.com/stolostron/hub-of-hubs-nonk8s-api)
[![Go Reference](https://pkg.go.dev/badge/github.com/stolostron/hub-of-hubs-nonk8s-api.svg)](https://pkg.go.dev/github.com/stolostron/hub-of-hubs-nonk8s-api)
[![License](https://img.shields.io/github/license/stolostron/hub-of-hubs-nonk8s-api)](/LICENSE)

The REST API component of [Hub-of-Hubs](https://github.com/stolostron/hub-of-hubs).

## Rationale

While a Kubernetes [Extension API server](https://kubernetes.io/docs/tasks/extend-kubernetes/setup-extension-api-server/) can be used to provide access 
to the items in the Hub-of-Hubs scalabale database, such a server would have the following drawbacks:

1. The API schema and URL parameters must confirm to the API schema of Kubernetes. In particular, no sort parameter could be passed for list operations (see [list options](https://github.com/kubernetes/apimachinery/blob/3d7c63b4de4fdee1917284129969901d4777facc/pkg/apis/meta/internalversion/types.go#L29), 
[parsed](https://github.com/kubernetes/apiserver/blob/cd64b6709ecf2514c0fc15965b3e34a4d7062308/pkg/endpoints/request/requestinfo.go#L212) by the API server code).
1. No advanced query capabilities (only label selectors of Kubernetes)
1. Watch from a revision version - hard to implmenent revision version mechanism for an SQL database (no build-in concept of revision versions in SQL)
1. The clients of a Kubernetes API server may try to cache all the resources, and can break as a result of caching a large number of resources.
1. Such API server must be REST (and not GRPC, for example).

## Environment variables

The following environment variables are required for the most tasks below:

* `REGISTRY`, for example `docker.io/vadimeisenbergibm`.
* `IMAGE_TAG`, for example `v0.1.0`.

## Build to run locally

```
make build
```

## Run Locally

Set the following environment variables:

* `DATABASE_URL` - the URL of the database server
* `CLUSTER_API_URL` - the URL of the Kubernetes API server
* `CLUSTER_API_CA_BUNDLE_PATH` - the CA bundle for the Kubernetes API server. If not provided, verification of the server certificates is skipped.
* `AUTHORIZATION_URL` - the URL of the authorization server
* `AUTHORIZATION_CA_BUNDLE_PATH` - the CA bundle for the authorization server. If not provided, verification of the server certificates is skipped.
* `KEY_PATH` - the path to the file that contains the private key for this server's TLS.
* `CERTIFICATE_PATH` - the path to the file that contains the certificate for this server's TLS.

Set the `DATABASE_URL` according to the PostgreSQL URL format: `postgres://YourUserName:YourURLEscapedPassword@YourHostname:5432/YourDatabaseName?sslmode=verify-full&pool_max_conns=50`.

:exclamation: Remember to URL-escape the password, you can do it in bash:

```
python -c "import sys, urllib as ul; print ul.quote_plus(sys.argv[1])" 'YourPassword'
```

Generate self-signed certificates:

```
mkdir testdata
openssl genrsa -out ./testdata/server.key 2048
openssl req -new -x509 -key ./testdata/server.key -out ./testdata/server.pem -days 365
```

Run the server
```
./bin/hub-of-hubs-nonk8s-api
```

## Build image

```
make build-images
```

## Deploy to a cluster

1.  Create a secret with your database url:

    ```
    kubectl create secret generic hub-of-hubs-database-secret --kubeconfig $TOP_HUB_CONFIG -n open-cluster-management --from-literal=url=$DATABASE_URL
    ```

1.  Deploy the operator:

    ```
    COMPONENT=$(basename $(pwd)) IMAGE_TAG=latest envsubst < deploy/operator.yaml.template | kubectl apply --kubeconfig $TOP_HUB_CONFIG -n open-cluster-management -f -
    ```

1.  Deploy Ingress:

    ```
    COMPONENT=$(basename $(pwd)) envsubst < deploy/ingress.yaml.template | kubectl apply --kubeconfig $TOP_HUB_CONFIG -n open-cluster-management -f -
    ```

### Test the ingress

Note that the port is 443 (the standard HTTPS port).

```
curl -ks  https://multicloud-console.apps.<the hub URL>/multicloud/hub-of-hubs-nonk8s-api/managedclusters  -H "Authorization: Bearer $TOKEN" | jq .[].metadata.name
```

### Working with Kubernetes deployment

Show log:

```
kubectl logs -l name=$(basename $(pwd)) -n open-cluster-management
```

Execute commands on the container:

```
kubectl exec -it $(kubectl get pod -l name=$(basename $(pwd)) -o jsonpath='{.items..metadata.name}' -n open-cluster-management) \
-n open-cluster-management -- bash
```

## Test (run the commands in this directory)

```
Add `example.com` to /etc/hosts as local host.
```

```
export TOKEN=<the OC token or Service Account token from its secret>
```

```
curl https://example.com:8080/managedclusters -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" --cacert ./certs/tls.crt
```

```
curl -s https://example.com:8080/managedclusters  -H "Authorization: Bearer $TOKEN" --cacert ./certs/tls.crt |
     jq .[].metadata.name
```
## Exercise the deployed API

1.  Define `TOKEN` and `CLUSTER_URL` environment variables

1.  Show the current identity:

    ```
    curl -k https://api.$CLUSTER_URL:6443/apis/user.openshift.io/v1/users/~ -H "Authorization: Bearer $TOKEN"
    ```

1.  Show the managed clusters in Non-Kubernetes REST API:

    ```
    curl -ks https://multicloud-console.apps.$CLUSTER_URL/multicloud/hub-of-hubs-nonk8s-api/managedclusters -H "Authorization: Bearer $TOKEN" | jq .[].metadata.name | sort
    ```

1.  Add a label `a=b`:

    ```
    curl -ks https://multicloud-console.apps.$CLUSTER_URL/multicloud/hub-of-hubs-nonk8s-api/managedclusters/cluster20 -lH "Authorization: Bearer $TOKEN" -H 'Accept: application/json' -X PATCH -d '[{"op":"add","path":"/metadata/labels/a","value":"b"}]]' -w "%{http_code}\n"
    ```
