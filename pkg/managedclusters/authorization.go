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
	"strings"

	opatypes "github.com/open-policy-agent/opa/server/types"
)

var errStatusNotOK = errors.New("response status not HTTP OK")

const (
	sqlFalse     = "FALSE"
	sqlTrue      = "TRUE"
	denyAll      = "WHERE " + sqlFalse
	termsTypeRef = "ref"
)

func filterByAuthorization(user string, groups []string, authorizationURL string, logWriter io.Writer) string {
	compileResponse, err := getPartialEvaluation(user, groups, authorizationURL)
	if err != nil {
		fmt.Fprintf(logWriter, "unable to get partial evaluation response %v\n", err)
		return denyAll
	}

	resultMap, ok := (*compileResponse.Result).(map[string]interface{})
	if !ok {
		fmt.Fprintf(logWriter, "unable to convert result to map\n")
		return denyAll
	}

	fmt.Fprintf(logWriter, "got partial evaluation response result %v\n", resultMap)

	queries, ok := resultMap["queries"].([]interface{})
	if !ok || len(queries) < 1 {
		return denyAll
	}

	var sb strings.Builder

	sb.WriteString("WHERE ")

	for _, rawQuery := range queries {
		query, ok := rawQuery.([]interface{})
		if !ok {
			fmt.Fprintf(logWriter, "unable to convert query to an array: %v\n", rawQuery)
			continue
		}

		if len(query) < 1 {
			continue
		}

		fmt.Fprintf(logWriter, "handle query: %v\n", query)

		sb.WriteString("(")
		for _, rawExpression := range query {
			expression, ok := rawExpression.(map[string]interface{})
			if !ok {
				fmt.Fprintf(logWriter, "unable to convert expression to a map: %v\n", rawExpression)
				sb.WriteString(sqlFalse + ") ")
				continue
			}

			fmt.Fprintf(logWriter, "handle expression: %v\n", expression)

			negated := false

			rawNegated, ok := expression["negated"]
			if ok {
				convertedNegated, ok := rawNegated.(bool)
				if ok {
					negated = convertedNegated
				}
			}

			rawTerms, ok := expression["terms"]
			if !ok {
				fmt.Fprintf(logWriter, "unable to get terms from expression: %v\n", expression)
				sb.WriteString(sqlFalse + ") ")
				continue
			}

			terms, ok := rawTerms.([]interface{})
			if !ok {
				fmt.Fprintf(logWriter, "unable to get terms array from expression: %v\n", expression)
				sb.WriteString(sqlFalse + ") ")
				continue
			}

			sb.WriteString("(")

			handleTermsArray(terms, negated, &sb, logWriter)

			sb.WriteString(") AND ")
		}
		sb.WriteString(sqlTrue) // TRUE to handle the last AND
		sb.WriteString(") OR ")
	}

	sb.WriteString(sqlFalse) // for the last OR

	returnString := sb.String()
	fmt.Fprintf(logWriter, "using SQL: %s\n", returnString)

	return returnString
}

func handleTermsArray(terms []interface{}, negated bool, sb *strings.Builder, logWriter io.Writer) {
	fmt.Fprintf(logWriter, "handle terms as an array (negated = %t): %v\n", negated, terms)

	if negated {
		sb.WriteString("NOT (")
	}

	expression, err := getSQLExpression(terms)
	if err == nil {
		sb.WriteString(expression)
	} else {
		fmt.Fprintf(logWriter, "unable to get SQL expression: %v\n", err)
		if negated {
			sb.WriteString(sqlTrue)
		} else {
			sb.WriteString(sqlFalse)
		}
	}
	if negated {
		sb.WriteString(")")
	}
}

func getSQLExpression(terms []interface{}) (string, error) {
	return "", errors.New("not implemented")
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
