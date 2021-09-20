// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	opatypes "github.com/open-policy-agent/opa/server/types"
)

var errStatusNotOK = errors.New("response status not HTTP OK")

func filterByAuthorization(user string, groups []string, authorizationURL string, logWriter io.Writer) string {
	compileResponse, err := getPartialEvaluation(user, groups, authorizationURL)
	if err != nil {
		fmt.Fprintf(logWriter, "unable to get partial evaluation response %v\n", err)
		return "WHERE FALSE"
	}

	resultMap, ok := (*compileResponse.Result).(map[string]interface{})
	if !ok {
		fmt.Fprintf(logWriter, "unable to convert result to map\n")
		return "WHERE FALSE"
	}

	fmt.Fprintf(logWriter, "got partial evaluation response result %v\n", resultMap)

	return "WHERE TRUE"
}

func getPartialEvaluation(user string, groups []string, authorizationURL string) (*opatypes.CompileResponseV1, error) {
	_ = groups // to be implemented later

	tr := &http.Transport{}
	client := &http.Client{Transport: tr}

	// the following two lines are required due to the fact that CompileRequestV1 uses
	// pointer to interface
	userInput := map[string]interface{}{"user": user}

	var input interface{} = userInput

	compileRequest := opatypes.CompileRequestV1{
		Input:    &input,
		Query:    "data.rbac.clusters.allow == true",
		Unknowns: &[]string{"input.cluster"},
	}

	jsonCompileRequest, err := json.Marshal(compileRequest)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal json: %w", err)
	}

	req, err := http.NewRequestWithContext(context.TODO(), "POST", fmt.Sprintf("%s/v1/compile",
		authorizationURL), bytes.NewBuffer(jsonCompileRequest))
	if err != nil {
		return nil, fmt.Errorf("unable to create request: %w", err)
	}

	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("got authentication error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", errStatusNotOK, resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read authentication response body: %w", err)
	}

	compileResponse := &opatypes.CompileResponseV1{}

	err = json.Unmarshal(body, compileResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshall json: %w", err)
	}

	return compileResponse, nil
}
