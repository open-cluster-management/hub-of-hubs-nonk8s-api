// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package managedclusters

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	opatypes "github.com/open-policy-agent/opa/server/types"
)

const (
	sqlFalse = "FALSE"
	sqlTrue  = "TRUE"

	denyAll  = "WHERE " + sqlFalse
	allowAll = "WHERE " + sqlTrue

	termTypeRef    = "ref"
	termTypeString = "string"
	termTypeVar    = "var"

	payloadField = "payload"

	negatedAttribute = "negated"
	termsAttribute   = "terms"

	inputVariable   = "input"
	clusterVariable = "cluster"

	opaQuery = "data.rbac.clusters.allow == true"

	termsArraySize                = 3 // should contain operator, first operand, second operand
	minReferencedVariablePathSize = 2 // must contain at least 'input.cluster'
)

var (
	errStatusNotOK               = errors.New("response status not HTTP OK")
	errUnknownOperator           = errors.New("unknown operator")
	errUnexpectedTermType        = errors.New("unexpected term type")
	errUnexpectedArraySize       = errors.New("unexpected array size")
	errUnexpectedTermsNumber     = errors.New("number of terms not as expected")
	errUnexpectedType            = errors.New("operand type not as expected")
	errUnexpectedValue           = errors.New("value not as expected")
	errMissingAttribute          = errors.New("missing attribute")
	errStringsBuilderWriteString = errors.New("strings.Builder WriteString returned error")
	errUnableToAppendCABundle    = errors.New("unable to append CA bundle")
)

func filterByAuthorization(user string, groups []string, authorizationURL string, authorizationCABundle []byte,
	certificate tls.Certificate, logWriter io.Writer) string {
	compileResponse, err := getPartialEvaluation(user, groups, authorizationURL, authorizationCABundle, certificate)
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

	writeStringOrDie(&sb, "WHERE ")

	for _, rawQuery := range queries {
		query, ok := rawQuery.([]interface{})
		if !ok {
			fmt.Fprintf(logWriter, "unable to convert query to an array: %v\n", rawQuery)
			continue
		}

		if len(queries) == 1 && len(query) == 0 {
			return allowAll
		}

		handleQuery(query, &sb, logWriter)
	}

	writeStringOrDie(&sb, sqlFalse) // for the last OR

	return sb.String()
}

func handleQuery(query []interface{}, sw io.StringWriter, logWriter io.Writer) {
	if len(query) < 1 {
		return
	}

	writeStringOrDie(sw, "(")

	for _, rawExpression := range query {
		handleExpression(rawExpression, sw, logWriter)
	}

	writeStringOrDie(sw, sqlTrue) // TRUE to handle the last AND
	writeStringOrDie(sw, ") OR ")
}

func handleExpression(rawExpression interface{}, sw io.StringWriter, logWriter io.Writer) {
	expression, ok := rawExpression.(map[string]interface{})
	if !ok {
		fmt.Fprintf(logWriter, "unable to convert expression to a map: %v\n", rawExpression)
		writeStringOrDie(sw, sqlFalse+") ")

		return
	}

	negated := false

	rawNegated, ok := expression[negatedAttribute]
	if ok {
		convertedNegated, ok := rawNegated.(bool)
		if ok {
			negated = convertedNegated
		}
	}

	rawTerms, ok := expression[termsAttribute]
	if !ok {
		fmt.Fprintf(logWriter, "unable to get terms from expression: %v\n", expression)
		writeStringOrDie(sw, sqlFalse+") ")

		return
	}

	terms, ok := rawTerms.([]interface{})
	if !ok {
		fmt.Fprintf(logWriter, "unable to get terms array from expression: %v\n", expression)
		writeStringOrDie(sw, sqlFalse+") ")

		return
	}

	writeStringOrDie(sw, "(")

	handleTermsArray(terms, negated, sw, logWriter)

	writeStringOrDie(sw, ") AND ")
}

// strings.Builder should not return errors.
func writeStringOrDie(sw io.StringWriter, s string) {
	if _, err := sw.WriteString(s); err != nil {
		panic(errStringsBuilderWriteString)
	}
}

func handleStringTerm(operandMap map[string]interface{}) (string, error) {
	termValue, err := getTermValue(operandMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operand's value: %w", err)
	}

	termValueString, ok := termValue.(string)
	if !ok {
		return "", fmt.Errorf("%w expected string, received %T", errUnexpectedType, termValue)
	}

	return "'" + termValueString + "'", nil
}

func handleRefTerm(operandMap map[string]interface{}) (string, error) {
	termValue, err := getTermValue(operandMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operand's value: %w", err)
	}

	termValueArray, ok := termValue.([]interface{})
	if !ok {
		return "", fmt.Errorf("%w expected array, received %T", errUnexpectedType, termValue)
	}

	termValueArrayLength := len(termValueArray)

	if termValueArrayLength < minReferencedVariablePathSize {
		return "", fmt.Errorf("%w expected %d or more, received %d", errUnexpectedTermsNumber,
			minReferencedVariablePathSize, termValueArrayLength)
	}

	firstPart, err := getTermStringValue(termValueArray[0], termTypeVar)
	if err != nil {
		return "", fmt.Errorf("unable to parse operand's first part: %w", err)
	}

	secondPart, err := getTermStringValue(termValueArray[1], termTypeString)
	if err != nil {
		return "", fmt.Errorf("unable to parse operand's second part: %w", err)
	}

	if firstPart != inputVariable && secondPart != clusterVariable {
		return "", fmt.Errorf("%w: expected 'input.cluster' received '%s.%s'", errUnexpectedValue, firstPart, secondPart)
	}

	operand, err := createPostgreSQLJSONPath(termValueArray[2:])
	if err != nil {
		return "", fmt.Errorf("unable to create PostgreSQL JSON Path expression: %w", err)
	}

	return operand, nil
}

func createPostgreSQLJSONPath(termValueArray []interface{}) (string, error) {
	operand := payloadField
	termValueArrayLength := len(termValueArray)

	for index, part := range termValueArray {
		partString, err := getTermStringValue(part, termTypeString)
		if err != nil {
			return "", fmt.Errorf("unable to parse operand's part: %w", err)
		}

		pathOperator := "->"
		if index >= termValueArrayLength-1 {
			pathOperator = "->>"
		}

		operand = operand + " " + pathOperator + " '" + partString + "'"
	}

	return operand, nil
}

func handleTermsArray(terms []interface{}, negated bool, sw io.StringWriter, logWriter io.Writer) {
	if negated {
		writeStringOrDie(sw, "NOT (")
	}

	expression, err := getSQLExpression(terms)
	if err == nil {
		writeStringOrDie(sw, expression)
	} else {
		fmt.Fprintf(logWriter, "unable to get SQL expression: %v\n", err)
		if negated {
			writeStringOrDie(sw, sqlTrue)
		} else {
			writeStringOrDie(sw, sqlFalse)
		}
	}

	if negated {
		writeStringOrDie(sw, ")")
	}
}

func getSQLExpression(terms []interface{}) (string, error) {
	if len(terms) != termsArraySize {
		return "", fmt.Errorf("%w: expected %d, received %d", errUnexpectedTermsNumber, termsArraySize, len(terms))
	}

	operator, err := getOperator(terms[0])
	if err != nil {
		return "", fmt.Errorf("unable to parse operator: %w", err)
	}

	sqlOperator := "="

	if operator != "eq" {
		return "", fmt.Errorf("%w %s", errUnknownOperator, operator)
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
		return "", fmt.Errorf("%w expected map, received %T", errUnexpectedType, term)
	}

	termType, err := getTermType(operandMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operand's type: %w", err)
	}

	switch termType {
	case termTypeString:
		operand, err := handleStringTerm(operandMap)
		if err != nil {
			return "", fmt.Errorf("unable to handle string term: %w", err)
		}

		return operand, nil
	case termTypeRef:
		operand, err := handleRefTerm(operandMap)
		if err != nil {
			return "", fmt.Errorf("unable to handle ref term: %w", err)
		}

		return operand, nil
	default:
		return "", fmt.Errorf("%w received %s", errUnexpectedTermType, termType)
	}
}

func getOperator(term interface{}) (string, error) {
	operatorMap, ok := term.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("%w: expected map, received %T", errUnexpectedType, term)
	}

	termType, err := getTermType(operatorMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operator's type: %w", err)
	}

	if termType != termTypeRef {
		return "", fmt.Errorf("%w: received %s", errUnexpectedTermType, termType)
	}

	termValue, err := getTermValue(operatorMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse operator's value: %w", err)
	}

	termValueArray, ok := termValue.([]interface{})
	if !ok {
		return "", fmt.Errorf("%w: expected array, received %T", errUnexpectedType, termValue)
	}

	if len(termValueArray) != 1 {
		return "", fmt.Errorf("%w: expected 1, received %d", errUnexpectedArraySize, len(termValueArray))
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
		return "", fmt.Errorf("%w: type", errMissingAttribute)
	}

	termTypeString, ok := termType.(string)
	if !ok {
		return "", fmt.Errorf("%w: expected string, received %T", errUnexpectedType, termType)
	}

	return termTypeString, nil
}

func getTermStringValue(term interface{}, expectedType string) (string, error) {
	termValueMap, ok := term.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("%w: expected map, received %T", errUnexpectedType, term)
	}

	termValueType, err := getTermType(termValueMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse term's value's type: %w", err)
	}

	if termValueType != expectedType {
		return "", fmt.Errorf("%w: expected %s, received %s", errUnexpectedTermType, expectedType, termValueType)
	}

	termValueValue, err := getTermValue(termValueMap)
	if err != nil {
		return "", fmt.Errorf("unable to parse term's value: %w", err)
	}

	termValueValueStr, ok := termValueValue.(string)
	if !ok {
		return "", fmt.Errorf("%w: expected string, received %T", errUnexpectedType, termValueValue)
	}

	return termValueValueStr, nil
}

func getTermValue(term map[string]interface{}) (interface{}, error) {
	value, ok := term["value"]
	if !ok {
		return "", fmt.Errorf("%w: value", errMissingAttribute)
	}

	return value, nil
}

func createClient(authorizationCABundle []byte, certificate tls.Certificate) (*http.Client, error) {
	tlsConfig := &tls.Config{
		//nolint:gosec
		InsecureSkipVerify: true,
	}

	if authorizationCABundle != nil {
		rootCAs := x509.NewCertPool()
		if ok := rootCAs.AppendCertsFromPEM(authorizationCABundle); !ok {
			return nil, fmt.Errorf("unable to append authorization CA Bundle: %w", errUnableToAppendCABundle)
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
			RootCAs:      rootCAs,
		}
	}

	tr := &http.Transport{TLSClientConfig: tlsConfig}

	return &http.Client{Transport: tr}, nil
}

func getPartialEvaluation(user string, groups []string, authorizationURL string,
	authorizationCABundle []byte, certificate tls.Certificate) (*opatypes.CompileResponseV1, error) {
	_ = groups // to be implemented later

	// the following two lines are required due to the fact that CompileRequestV1 uses
	// pointer to interface
	userInput := map[string]interface{}{"user": user}

	var input interface{} = userInput

	compileRequest := opatypes.CompileRequestV1{
		Input:    &input,
		Query:    opaQuery,
		Unknowns: &[]string{fmt.Sprintf("%s.%s", inputVariable, clusterVariable)},
	}

	jsonCompileRequest, err := json.Marshal(compileRequest)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal json: %w", err)
	}

	client, err := createClient(authorizationCABundle, certificate)
	if err != nil {
		return nil, fmt.Errorf("unable to create client: %w", err)
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
