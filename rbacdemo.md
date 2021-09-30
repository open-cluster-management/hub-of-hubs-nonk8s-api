# RBAC demo with OPA


1.  Show the clusters in the DB

    ```
    select payload -> 'metadata' -> 'labels' as labels from status.managed_clusters ORDER BY payload -> 'metadata' ->>'name';
    ```

    Output:

    ```
    labels
    ---------------------------------------------------------------------
    {"name": "cluster0", "vendor": "Kind", "environment": "production"}
    {"name": "cluster1", "vendor": "Kind", "environment": "production"}
    {"name": "cluster2", "vendor": "Kind", "environment": "production"}
    {"name": "cluster3", "vendor": "Kind", "environment": "dev"}
    {"name": "cluster4", "vendor": "Kind"}
    {"name": "cluster5", "vendor": "Kind", "environment": "production"}
    {"name": "cluster6", "vendor": "Kind", "environment": "production"}
    {"name": "cluster7", "vendor": "Kind"}
    {"name": "cluster8", "vendor": "Kind"}
    {"name": "cluster9", "vendor": "Kind", "environment": "dev"}
    ```

1.  Show some SQL queries on the table:

    ```
    SELECT payload -> 'metadata' ->> 'name' FROM status.managed_clusters WHERE
    payload -> 'metadata' -> 'labels' ->> 'environment' = 'dev';
    ```

    ```
    SELECT payload -> 'metadata' ->> 'name' FROM status.managed_clusters WHERE
    payload -> 'metadata' -> 'labels' ->> 'environment' = 'production';
    ```

1.  Show the current identity:

    ```
    curl -k https://api.veisenbe-hoh.dev10.red-chesterfield.com:6443/apis/user.openshift.io/v1/users/~ -H "Authorization: Bearer $TOKEN"
    ```

1.  Start the OPA server (in the hub-of-hubs-rbac directory):

    ```
    opa run --server ./*.rego testdata/*.json
    ```

1.  Start the REST API server:

    ```
    ./bin/hub-of-hubs-nonk8s-api
    ```

1.  Get the list of the managed clusters:

    ```
    curl -ks https://localhost:8080/managedclusters  -H "Authorization: Bearer $TOKEN" | jq .[].metadata.name | sort
    ```
