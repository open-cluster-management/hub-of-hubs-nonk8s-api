[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Hub-of-Hubs Spec Sync

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

* DATABASE_URL

Set the `DATABASE_URL` according to the PostgreSQL URL format: `postgres://YourUserName:YourURLEscapedPassword@YourHostname:5432/YourDatabaseName?sslmode=verify-full&pool_max_conns=50`.

:exclamation: Remember to URL-escape the password, you can do it in bash:

```
python -c "import sys, urllib as ul; print ul.quote_plus(sys.argv[1])" 'YourPassword'
```

```
./bin/hub-of-hubs-nonk8s-api --kubeconfig $TOP_HUB_CONFIG
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
    COMPONENT=$(basename $(pwd)) envsubst < deploy/operator.yaml.template | kubectl apply --kubeconfig $TOP_HUB_CONFIG -n open-cluster-management -f -
    ```
