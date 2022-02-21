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
	syncIntervalInSeconds              = 4
	onlyPatchOfLabelsIsImplemented     = "only patch of labels is currently implemented"
	onlyAddOrRemoveAreImplemented      = "only add or remove operations are currently implemented"
	optimisticConcurrencyRetryAttempts = 5
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

		if !isAuthorized(user, groups, authorizationURL, authorizationCABundle, dbConnectionPool, cluster) {
			ginCtx.JSON(http.StatusForbidden, gin.H{"status": "the current user cannot patch the cluster"})
		}

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

		retryAttempts := optimisticConcurrencyRetryAttempts

		for retryAttempts > 0 {
			err = updateLabels(cluster, labelsToAdd, labelsToRemove, dbConnectionPool)
			if err == nil {
				break
			}

			retryAttempts--
		}

		if err != nil {
			ginCtx.String(http.StatusInternalServerError, "internal error")
			fmt.Fprintf(gin.DefaultWriter, "error in updating managed cluster labels: %v\n", err)
		}
	}
}

func updateLabels(cluster string, labelsToAdd map[string]string, labelsToRemove map[string]struct{},
	dbConnectionPool *pgxpool.Pool) error {
	if len(labelsToAdd) == 0 && len(labelsToRemove) == 0 {
		return nil
	}

	rows, err := dbConnectionPool.Query(context.TODO(),
		"SELECT labels, deleted_label_keys, version from spec.managed_clusters_labels WHERE managed_cluster_name = $1",
		cluster)
	if err != nil {
		return fmt.Errorf("failed to read from managed_clusters_labels: %w", err)
	}
	defer rows.Close()

	if !rows.Next() { // insert the labels
		_, err := dbConnectionPool.Exec(context.TODO(),
			`INSERT INTO spec.managed_clusters_labels (managed_cluster_name, labels,
			deleted_label_keys, version, updated_at) values($1, $2::jsonb, $3::jsonb, 0, now())`,
			cluster, labelsToAdd, getKeys(labelsToRemove))
		if err != nil {
			return fmt.Errorf("failed to insert into the managed_clusters_labels table: %w", err)
		}

		return nil
	}

	var (
		currentLabelsToAdd         map[string]string
		currentLabelsToRemoveSlice []string
		version                    int64
	)

	err = rows.Scan(&currentLabelsToAdd, &currentLabelsToRemoveSlice, &version)
	if err != nil {
		return fmt.Errorf("failed to scan a row: %w", err)
	}

	err = updateRow(cluster, labelsToAdd, currentLabelsToAdd, labelsToRemove, getMap(currentLabelsToRemoveSlice),
		version, dbConnectionPool)
	if err != nil {
		return fmt.Errorf("failed to update managed_clusters_labels table: %w", err)
	}

	// assumimg there is a single row
	if rows.Next() {
		fmt.Fprintf(gin.DefaultWriter, "Warning: more than one row for cluster %s\n", cluster)
	}

	return nil
}

func updateRow(cluster string, labelsToAdd map[string]string, currentLabelsToAdd map[string]string,
	labelsToRemove map[string]struct{}, currentLabelsToRemove map[string]struct{},
	version int64, dbConnectionPool *pgxpool.Pool) error {
	newLabelsToAdd := make(map[string]string)
	newLabelsToRemove := make(map[string]struct{})

	for key := range currentLabelsToRemove {
		if _, keyToBeAdded := labelsToAdd[key]; !keyToBeAdded {
			newLabelsToRemove[key] = struct{}{}
		}
	}

	for key := range labelsToRemove {
		newLabelsToRemove[key] = struct{}{}
	}

	for key, value := range currentLabelsToAdd {
		if _, keyToBeRemoved := labelsToRemove[key]; !keyToBeRemoved {
			newLabelsToAdd[key] = value
		}
	}

	for key, value := range labelsToAdd {
		newLabelsToAdd[key] = value
	}

	_, err := dbConnectionPool.Exec(context.TODO(),
		`UPDATE spec.managed_clusters_labels SET
		labels = $1::jsonb,
		deleted_label_keys = $2::jsonb,
		version = version + 1,
		updated_at = now()
		WHERE managed_cluster_name=$3 AND version=$4`,
		newLabelsToAdd, getKeys(newLabelsToRemove), cluster, version)
	if err != nil {
		return fmt.Errorf("failed to insert a row: %w", err)
	}

	return nil
}

func getMap(aSlice []string) map[string]struct{} {
	mapToReturn := make(map[string]struct{}, len(aSlice))

	for _, key := range aSlice {
		mapToReturn[key] = struct{}{}
	}

	return mapToReturn
}

// from https://stackoverflow.com/q/21362950
func getKeys(aMap map[string]struct{}) []string {
	keys := make([]string, len(aMap))
	index := 0

	for key := range aMap {
		keys[index] = key
		index++
	}

	return keys
}

func isAuthorized(user string, groups []string, authorizationURL string, authorizationCABundle []byte,
	dbConnectionPool *pgxpool.Pool, cluster string) bool {
	query := fmt.Sprintf(
		"SELECT COUNT(payload) from status.managed_clusters WHERE payload -> 'metadata' ->> 'name' = '%s' AND %s",
		cluster, filterByAuthorization(user, groups, authorizationURL, authorizationCABundle, gin.DefaultWriter))

	var count int64

	err := dbConnectionPool.QueryRow(context.TODO(), query).Scan(&count)
	if err != nil {
		fmt.Fprintf(gin.DefaultWriter, "error in quering managed clusters: %v\n", err)
		return false
	}

	return count > 0
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
		rawLabel := strings.TrimPrefix(aPatch.Path, "/metadata/labels/")

		if rawLabel == aPatch.Path {
			ginCtx.JSON(http.StatusNotImplemented, gin.H{"status": onlyPatchOfLabelsIsImplemented})

			return nil, nil, errOnlyPatchOfLabelsIsImplemented
		}

		label := strings.Replace(rawLabel, "~", "/", 1)
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
	return "SELECT payload FROM status.managed_clusters WHERE TRUE AND " +
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
