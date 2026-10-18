// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"net/url"
)

// AttachGroupPolicy attaches a canned policy to an IAM group.
func (c *RustfsAdmin) AttachGroupPolicy(group, policy string) error {
	query := url.Values{}
	query.Set("userOrGroup", group)
	query.Set("policyName", policy)
	query.Set("isGroup", "true")
	reqData := RequestData{
		Method:      "PUT",
		RelPath:     "set-user-or-group-policy",
		QueryValues: query,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// DetachGroupPolicy detaches a canned policy from an IAM group. The admin API
// exposes no DELETE on set-user-or-group-policy; an empty policyName removes
// the policy mapping (same semantics as MinIO set-policy with an empty name).
func (c *RustfsAdmin) DetachGroupPolicy(group, policy string) error {
	query := url.Values{}
	query.Set("userOrGroup", group)
	query.Set("policyName", "")
	query.Set("isGroup", "true")
	reqData := RequestData{
		Method:      "PUT",
		RelPath:     "set-user-or-group-policy",
		QueryValues: query,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}
