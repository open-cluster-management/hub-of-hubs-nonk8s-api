module github.com/open-cluster-management/hub-of-hubs-nonk8s-api

go 1.16

require (
	github.com/gin-gonic/gin v1.7.4
	github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr v0.2.0
	github.com/jackc/pgx/v4 v4.11.0
	github.com/open-cluster-management/api v0.0.0-20210527013639-a6845f2ebcb1
	github.com/openshift/api v3.9.0+incompatible
	go.uber.org/zap v1.19.0
	k8s.io/apimachinery v0.20.5
	k8s.io/client-go v0.20.5
)
