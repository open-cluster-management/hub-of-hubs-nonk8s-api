[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Hub-of-Hubs Nonk8s API

[![Go Report Card](https://goreportcard.com/badge/github.com/open-cluster-management/hub-of-hubs-spec-sync)](https://goreportcard.com/report/github.com/open-cluster-management/hub-of-hubs-nonk8s-api)
[![License](https://img.shields.io/github/license/open-cluster-management/hub-of-hubs-nonk8s-api)](/LICENSE)

The REST API component of [Hub-of-Hubs](https://github.com/open-cluster-management/hub-of-hubs).

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
