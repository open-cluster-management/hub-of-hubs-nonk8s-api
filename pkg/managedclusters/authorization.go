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
	allowAll     = "WHERE " + sqlTrue
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

		if len(queries) == 1 && len(query) == 0 {
			return allowAll
		}

		if len(query) < 1 {
			continue
		}

		sb.WriteString("(")
		for _, rawExpression := range query {
			expression, ok := rawExpression.(map[string]interface{})
			if !ok {
				fmt.Fprintf(logWriter, "unable to convert expression to a map: %v\n", rawExpression)
				sb.WriteString(sqlFalse + ") ")
				continue
			}

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

	return sb.String()
}

func handleTermsArray(terms []interface{}, negated bool, sb *strings.Builder, logWriter io.Writer) {
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
	if len(terms) != 3 {
		return "", errors.New("number of terms is not three as expected, received " + string(len(terms)))
	}

	operator, err := getOperator(terms[0])
	if err != nil {
		return "", fmt.Errorf("unable to parse operator: %w", err)
	}

	sqlOperator := "="
	if operator != "eq" {
		return "", errors.New("Unknown operator " + operator)
	}

	firstOperand, err := getOperand(terms[1])
	if err != nil {
		return "", fmt.Errorf("unable to parse first operand: %w", err)
	}

	secondOperand, err := getOperand(terms[2])
	if err != nil {
		return "", fmt.Errorf("unable to parse second operand: %w", err)
	}

	return firstOperand + " " + sqlOperator + " " + secondOperand, nil
}

func getOperand(term interface{}) (string, error) {
	operandMap, ok := term.(map[string]interface{})
	if !ok {
		return "", errors.New("operand term is not map")
	}

	termType, err := getTermType(operandMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operand's type: %w", err)
	}

	switch termType {
	case "string":
		termValue, err := getTermValue(operandMap)
		if err != nil {
			return "", fmt.Errorf("unable to parse operand's value: %w", err)
		}
		termValueString, ok := termValue.(string)
		if !ok {
			return "", errors.New("operand's value for type string is not a string")
		}
		return "'" + termValueString + "'", nil

	case "ref":
		termValue, err := getTermValue(operandMap)
		if err != nil {
			return "", fmt.Errorf("unable to parse operand's value: %w", err)
		}

		termValueArray, ok := termValue.([]interface{})
		if !ok {
			return "", errors.New("operand's value for type ref is not an array")
		}

		termValueArrayLength := len(termValueArray)

		if termValueArrayLength < 2 {
			return "", errors.New("number of ref terms is not less than two, received " +
				string(len(termValueArray)))
		}

		firstPart, err := getTermStringValue(termValueArray[0], "var")
		if err != nil {
			return "", fmt.Errorf("unable to parse operand's first part: %w", err)
		}

		secondPart, err := getTermStringValue(termValueArray[1], "string")
		if err != nil {
			return "", fmt.Errorf("unable to parse operand's second part: %w", err)
		}

		if firstPart != "input" && secondPart != "cluster" {
			return "", errors.New("ref term is not input.cluster, received: " + firstPart + ":" + secondPart)
		}

		operand := "payload"

		for index, part := range termValueArray[2:] {
			partString, err := getTermStringValue(part, "string")
			if err != nil {
				return "", fmt.Errorf("unable to parse operand's part: %w", err)
			}

			pathOperator := "->"
			if index >= termValueArrayLength-3 {
				pathOperator = "->>"
			}

			operand = operand + " " + pathOperator + " '" + partString + "'"
		}

		return operand, nil
	default:
		return "", errors.New("unknown operand's type")
	}
}

func getOperator(term interface{}) (string, error) {
	operatorMap, ok := term.(map[string]interface{})
	if !ok {
		return "", errors.New("operator term is not map")
	}

	termType, err := getTermType(operatorMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operator's type: %w", err)
	}

	if termType != "ref" {
		return "", errors.New("operator term's type is not ref")
	}

	termValue, err := getTermValue(operatorMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operator's value: %w", err)
	}

	termValueArray, ok := termValue.([]interface{})
	if !ok {
		return "", errors.New("operator term's value is not array")
	}

	if len(termValueArray) != 1 {
		return "", errors.New("operator term's value array size is not 1")
	}

	termValueValueStr, err := getTermStringValue(termValueArray[0], "var")
	if err != nil {
		return "", fmt.Errorf("unable to parse term's value value: %w", err)
	}

	return termValueValueStr, nil
}

func getTermType(term map[string]interface{}) (string, error) {
	termType, ok := term["type"]
	if !ok {
		return "", errors.New("no type in term")
	}

	termTypeString, ok := termType.(string)
	if !ok {
		return "", errors.New("type is not string")
	}

	return termTypeString, nil
}

func getTermStringValue(term interface{}, expectedType string) (string, error) {
	termValueMap, ok := term.(map[string]interface{})
	if !ok {
		return "", errors.New("term's value is not a map")
	}

	termValueType, err := getTermType(termValueMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse term's value's type: %w", err)
	}

	if termValueType != expectedType {
		return "", errors.New("wrong term value's type, expected " + expectedType + " got " + termValueType)
	}

	termValueValue, err := getTermValue(termValueMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse term's value: %w", err)
	}

	termValueValueStr, ok := termValueValue.(string)
	if !ok {
		return "", errors.New("term's value is not a string")
	}

	return termValueValueStr, nil
}

func getTermValue(term map[string]interface{}) (interface{}, error) {
	value, ok := term["value"]
	if !ok {
		return "", errors.New("no value in term")
	}

	return value, nil
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
