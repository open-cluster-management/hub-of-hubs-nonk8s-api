// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	set "github.com/deckarep/golang-set"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v4/pgxpool"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	"github.com/stolostron/hub-of-hubs-nonk8s-api/pkg/authentication"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	syncIntervalInSeconds          = 4
	onlyPatchOfLabelsIsImplemented = "only patch of labels is currently implemented"
	onlyAddOrRemoveAreImplemented  = "only add or remove operations are currently implemented"
)

var (
	errOnlyPatchOfLabelsIsImplemented = errors.New(onlyPatchOfLabelsIsImplemented)
	errOnlyAddOrRemoveAreImplemented  = errors.New(onlyAddOrRemoveAreImplemented)
)

type patch struct {
	Op    string `json:"op" binding:"required"`
	Path  string `json:"path" binding:"required"`
	Value string `json:"value"`
}

// Patch middleware.
func Patch(authorizationURL string, authorizationCABundle []byte,
	dbConnectionPool *pgxpool.Pool) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		user, isCorrectType := ginCtx.MustGet(authentication.UserKey).(string)
		if !isCorrectType {
			fmt.Fprintf(gin.DefaultWriter, "unable to get user from context")

			user = "Unknown"
		}

		groups, isCorrectType := ginCtx.MustGet(authentication.GroupsKey).([]string)
		if !isCorrectType {
			fmt.Fprintf(gin.DefaultWriter, "unable to get groups from context")

			groups = []string{}
		}

		cluster := ginCtx.Param("cluster")

		fmt.Fprintf(gin.DefaultWriter, "patch for cluster: %s\n", cluster)

		fmt.Fprintf(gin.DefaultWriter, "got authenticated user: %v\n", user)
		fmt.Fprintf(gin.DefaultWriter, "user groups: %v\n", groups)

		var patches []patch

		err := ginCtx.BindJSON(&patches)
		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "failed to bind: %s\n", err.Error())
			return
		}

		labelsToAdd, labelsToRemove, err := getLabels(ginCtx, patches)
		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "failed to get labels: %s\n", err.Error())
			return
		}

		fmt.Fprintf(gin.DefaultWriter, "labels to add: %v\n", labelsToAdd)
		fmt.Fprintf(gin.DefaultWriter, "labels to remove: %v\n", labelsToRemove)
	}
}

func getLabels(ginCtx *gin.Context, patches []patch) (map[string]string, map[string]struct{}, error) {
	labelsToAdd := make(map[string]string)
	labelsToRemove := make(map[string]struct{})

	// from https://datatracker.ietf.org/doc/html/rfc6902:
	// Evaluation of a JSON Patch document begins against a target JSON
	// document.  Operations are applied sequentially in the order they
	// appear in the array.  Each operation in the sequence is applied to
	// the target document; the resulting document becomes the target of the
	// next operation.  Evaluation continues until all operations are
	// successfully applied or until an error condition is encountered.

	for _, aPatch := range patches {
		label := strings.TrimPrefix(aPatch.Path, "/metadata/labels/")

		if label == aPatch.Path {
			ginCtx.JSON(http.StatusNotImplemented, gin.H{"status": onlyPatchOfLabelsIsImplemented})

			return nil, nil, errOnlyPatchOfLabelsIsImplemented
		}

		if aPatch.Op == "add" {
			delete(labelsToRemove, label)

			labelsToAdd[label] = aPatch.Value

			continue
		}

		if aPatch.Op == "remove" {
			delete(labelsToAdd, label)

			labelsToRemove[label] = struct{}{}

			continue
		}

		ginCtx.JSON(http.StatusNotImplemented, gin.H{"status": onlyAddOrRemoveAreImplemented})

		return nil, nil, errOnlyAddOrRemoveAreImplemented
	}

	return labelsToAdd, labelsToRemove, nil
}

// Get middleware.
func Get(authorizationURL string, authorizationCABundle []byte,
	dbConnectionPool *pgxpool.Pool) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		user, isCorrectType := ginCtx.MustGet(authentication.UserKey).(string)
		if !isCorrectType {
			fmt.Fprintf(gin.DefaultWriter, "unable to get user from context")

			user = "Unknown"
		}

		groups, isCorrectType := ginCtx.MustGet(authentication.GroupsKey).([]string)
		if !isCorrectType {
			fmt.Fprintf(gin.DefaultWriter, "unable to get groups from context")

			groups = []string{}
		}

		fmt.Fprintf(gin.DefaultWriter, "got authenticated user: %v\n", user)
		fmt.Fprintf(gin.DefaultWriter, "user groups: %v\n", groups)

		query := sqlQuery(user, groups, authorizationURL, authorizationCABundle)
		fmt.Fprintf(gin.DefaultWriter, "query: %v\n", query)

		if _, watch := ginCtx.GetQuery("watch"); watch {
			handleRowsForWatch(ginCtx, query, dbConnectionPool)
			return
		}

		handleRows(ginCtx, query, dbConnectionPool)
	}
}

func sqlQuery(user string, groups []string, authorizationURL string, authorizationCABundle []byte) string {
	return "SELECT payload FROM status.managed_clusters " +
		filterByAuthorization(user, groups, authorizationURL, authorizationCABundle, gin.DefaultWriter)
}

func handleRowsForWatch(ginCtx *gin.Context, query string, dbConnectionPool *pgxpool.Pool) {
	writer := ginCtx.Writer
	header := writer.Header()
	header.Set("Transfer-Encoding", "chunked")
	header.Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(syncIntervalInSeconds * time.Second)

	ctx, cancelContext := context.WithCancel(context.Background())
	defer cancelContext()

	// TODO - add deleted field to the status.managed_clusters table
	// instead of holding the previously added managed clusters by memory
	// and calculating the deleted clusters
	previouslyAddedManagedClusterNames := set.NewSet()

	for {
		select {
		case <-writer.CloseNotify():
			ticker.Stop()
			cancelContext()

			return
		case <-ticker.C:
			if ginCtx.Err() != nil || ginCtx.IsAborted() {
				ticker.Stop()
				cancelContext()

				return
			}

			doHandleRowsForWatch(ctx, writer, query, dbConnectionPool, previouslyAddedManagedClusterNames)
		}
	}
}

func doHandleRowsForWatch(ctx context.Context, writer io.Writer, query string, dbConnectionPool *pgxpool.Pool,
	previouslyAddedManagedClusterNames set.Set) {
	rows, err := dbConnectionPool.Query(ctx, query)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "error in quering managed clusters: %v\n", err)
	}

	addedManagedClusterNames := set.NewSet()

	for rows.Next() {
		managedCluster := &clusterv1.ManagedCluster{}

		err := rows.Scan(managedCluster)
		if err != nil {
			continue
		}

		addedManagedClusterNames.Add(managedCluster.GetName())
		sendWatchEvent(&metav1.WatchEvent{Type: "ADDED", Object: runtime.RawExtension{Object: managedCluster}}, writer)
	}

	managedClusterNamesToDelete := previouslyAddedManagedClusterNames.Difference(addedManagedClusterNames)

	managedClusterNamesToDeleteIterator := managedClusterNamesToDelete.Iterator()
	for managedClusterNameToDelete := range managedClusterNamesToDeleteIterator.C {
		managedClusterNameToDeleteAsString, ok := managedClusterNameToDelete.(string)
		if !ok {
			continue
		}

		previouslyAddedManagedClusterNames.Remove(managedClusterNameToDeleteAsString)

		managedClusterToDelete := &clusterv1.ManagedCluster{}
		managedClusterToDelete.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   clusterv1.GroupVersion.Group,
			Version: clusterv1.GroupVersion.Version,
			Kind:    "ManagedCluster",
		})
		managedClusterToDelete.SetName(managedClusterNameToDeleteAsString)
		sendWatchEvent(&metav1.WatchEvent{Type: "DELETED", Object: runtime.RawExtension{Object: managedClusterToDelete}},
			writer)
	}

	managedClusterNamesToAdd := addedManagedClusterNames.Difference(previouslyAddedManagedClusterNames)

	managedClusterNamesToAddIterator := managedClusterNamesToAdd.Iterator()
	for managedClusterNameToAdd := range managedClusterNamesToAddIterator.C {
		managedClusterNameToAddAsString, ok := managedClusterNameToAdd.(string)
		if !ok {
			continue
		}

		previouslyAddedManagedClusterNames.Add(managedClusterNameToAddAsString)
	}

	writer.(http.Flusher).Flush()
}

func sendWatchEvent(watchEvent *metav1.WatchEvent, writer io.Writer) {
	json, err := json.Marshal(watchEvent)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "error in json marshalling: %v\n", err)
		return
	}

	_, err = writer.Write(json)

	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "error in writing response: %v\n", err)
		return
	}

	_, err = writer.Write([]byte("\n"))

	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "error in writing response: %v\n", err)
		return
	}
}

func handleRows(ginCtx *gin.Context, query string, dbConnectionPool *pgxpool.Pool) {
	rows, err := dbConnectionPool.Query(context.TODO(), query)
	if err != nil {
		ginCtx.String(http.StatusInternalServerError, "internal error")
		fmt.Fprintf(gin.DefaultWriter, "error in quering managed clusters: %v\n", err)
	}

	managedClusters := []*clusterv1.ManagedCluster{}

	for rows.Next() {
		managedCluster := &clusterv1.ManagedCluster{}

		err := rows.Scan(managedCluster)
		if err != nil {
			fmt.Fprintf(gin.DefaultWriter, "error in scanning a managed cluster: %v\n", err)
			continue
		}

		managedClusters = append(managedClusters, managedCluster)
	}

	ginCtx.JSON(http.StatusOK, managedClusters)
}
