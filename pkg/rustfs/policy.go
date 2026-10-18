package rustfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

type PolicyStatement struct {
	Effect   string   `json:"Effect"`
	Action   []string `json:"Action"`
	Resource []string `json:"Resource,omitempty"`
}

type PolicyStatementSinle struct {
	Effect   string   `json:"Effect"`
	Action   string   `json:"Action"`
	Resource []string `json:"Resource"`
}

type Policy struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
	Name      string
}

type statementReply struct {
	Statement []PolicyStatement `json:"Statement"`
}

type statementReplySingle struct {
	Statement []PolicyStatementSinle `json:"Statement"`
}

type policyReply struct {
	PolicyName string          `json:"policy_name"`
	Policy     json.RawMessage `json:"policy"`
}

type policyPost struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

func (c *RustfsAdmin) CreatePolicy(policy Policy) error {
	urlValues := make(url.Values)
	urlValues.Set("name", policy.Name)
	policy.Version = "2012-10-17" // only this is working
	post := policyPost{
		Version:   policy.Version,
		Statement: policy.Statement,
	}
	bytes, err := json.Marshal(post)
	if err != nil {
		return err
	}

	req_data := RequestData{
		Method:      "PUT",
		RelPath:     "add-canned-policy",
		Content:     bytes,
		QueryValues: urlValues,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// parsePolicyField decodes the "policy" field of an info-canned-policy reply
// into statements.
//
// The RustFS API may return `policy` either as a JSON string containing the
// policy document, or as an inline JSON object, so the field is normalized to
// bytes first. A document whose Action is a single string (rather than a list)
// is accepted as well. The document comes from the server and is therefore
// untrusted: malformed input must produce an error, never a panic.
func parsePolicyField(policy json.RawMessage) ([]PolicyStatement, error) {
	policyBytes := []byte(policy)
	if len(policyBytes) > 0 && policyBytes[0] == '"' {
		var asString string
		if err := json.Unmarshal(policyBytes, &asString); err != nil {
			return nil, err
		}
		policyBytes = []byte(asString)
	}

	var statement statementReply
	errStatement := json.NewDecoder(bytes.NewReader(policyBytes)).Decode(&statement)
	if errStatement == nil {
		return statement.Statement, nil
	}

	var statementSingle statementReplySingle
	errSingle := json.NewDecoder(bytes.NewReader(policyBytes)).Decode(&statementSingle)
	if errSingle == nil {
		statements := make([]PolicyStatement, 0, len(statementSingle.Statement))
		for _, got := range statementSingle.Statement {
			statements = append(statements, PolicyStatement{
				Effect:   got.Effect,
				Action:   []string{got.Action},
				Resource: got.Resource,
			})
		}
		return statements, nil
	}

	return nil, errors.Join(errStatement, errSingle)
}

func (c *RustfsAdmin) ReadPolicy(policy string) (Policy, error) {
	var instance policyReply
	var read Policy
	urlValues := make(url.Values)
	urlValues.Set("name", policy)
	req_data := RequestData{
		Method:      "GET",
		RelPath:     "info-canned-policy",
		QueryValues: urlValues,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return Policy{}, err
	}
	defer drainClose(resp)

	err = json.NewDecoder(resp.Body).Decode(&instance)
	if err != nil {
		return Policy{}, err
	}
	read.Name = instance.PolicyName
	read.Version = "2012-10-17"

	statements, err := parsePolicyField(instance.Policy)
	if err != nil {
		return Policy{}, err
	}
	read.Statement = statements
	return read, nil
}

func (c *RustfsAdmin) DeletePolicy(policy string) error {

	urlValues := make(url.Values)
	urlValues.Set("name", policy)
	req_data := RequestData{
		Method:      "DELETE",
		RelPath:     "remove-canned-policy",
		QueryValues: urlValues,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil

}
