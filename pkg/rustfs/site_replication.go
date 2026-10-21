// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
)

// SiteReplicationSite models a peer site as accepted by the
// site-replication/add and site-replication/edit admin endpoints. Wire field
// names follow the RustFS (MinIO-compatible) site-replication protocol.
type SiteReplicationSite struct {
	Name          string `json:"name"`
	Endpoint      string `json:"endpoints"`
	AccessKey     string `json:"accessKey,omitempty"`
	SecretKey     string `json:"secretKey,omitempty"`
	SkipTLSVerify bool   `json:"skipTlsVerify,omitempty"`
	CACertPEM     string `json:"caCertPem,omitempty"`
}

// SiteReplicationPeer is a peer site as reported by the site-replication/info
// endpoint.
type SiteReplicationPeer struct {
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	DeploymentID  string `json:"deploymentID"`
	SkipTLSVerify bool   `json:"skipTlsVerify"`
}

// SiteReplicationInfo is the response of the site-replication/info endpoint.
type SiteReplicationInfo struct {
	Enabled bool                  `json:"enabled"`
	Name    string                `json:"name"`
	Sites   []SiteReplicationPeer `json:"sites"`
}

// SiteReplicationStatus is the response of the add/remove/edit endpoints.
type SiteReplicationStatus struct {
	Status      string `json:"status"`
	ErrorDetail string `json:"errorDetail"`
}

// SiteReplicationResync is the response of the site-replication/resync/op
// endpoint.
type SiteReplicationResync struct {
	OpType string `json:"op"`
	ID     string `json:"id"`
	Status string `json:"status"`
	State  string `json:"state"`
}

func (c *RustfsAdmin) SiteReplicationAdd(site SiteReplicationSite) error {
	//#nosec G117 — AccessKey is a public identifier, not a secret
	body, err := json.Marshal([]SiteReplicationSite{site})
	if err != nil {
		return err
	}
	return c.siteReplicationWrite("site-replication/add", body)
}

// SiteReplicationPeerEdit is the wire shape of the public site-replication/edit
// route. RustFS parses a single PeerInfo object there — unlike
// site-replication/add, which takes an array of SiteReplicationSite — and names
// the endpoint field "endpoint" (singular). Verified against RustFS 1.0.3: the
// array/plural shape the add route accepts is rejected by the edit route with
// "invalid JSON: invalid type: map, expected a string at line 1 column 1", and
// "endpoints" leaves the endpoint empty
// ("invalid site endpoint “: relative URL without a base").
//
// The route is the public edit: it updates the local topology and fans the
// change out to every peer. The similarly named site-replication/peer/edit is
// the peer-side delivery used by that fan-out and must not be called directly:
// it only rewrites the receiving site and leaves the rest of the group
// diverged.
type SiteReplicationPeerEdit struct {
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	DeploymentID  string `json:"deploymentID,omitempty"`
	AccessKey     string `json:"accessKey,omitempty"`
	SecretKey     string `json:"secretKey,omitempty"`
	SkipTLSVerify bool   `json:"skipTlsVerify,omitempty"`
	CACertPEM     string `json:"caCertPem,omitempty"`
}

// EditPeer edits one peer across the site-replication group. Changing a peer's
// endpoint or TLS settings in place is what the existing rustfs_site_replication
// resource does on update, so Terraform does not have to destroy and recreate
// the group.
func (c *RustfsAdmin) EditPeer(peer SiteReplicationPeerEdit) error {
	//#nosec G117 — AccessKey is a public identifier, not a secret
	body, err := json.Marshal(peer)
	if err != nil {
		return err
	}
	return c.siteReplicationWrite("site-replication/edit", body)
}

// RemovePeer removes peers from the site-replication group. The public remove
// route notifies every remaining peer; site-replication/peer/remove is the
// peer-side delivery used by that notification and is not a substitute.
func (c *RustfsAdmin) RemovePeer(names []string) error {
	body, err := json.Marshal(struct {
		Sites []string `json:"sites"`
	}{Sites: names})
	if err != nil {
		return err
	}
	return c.siteReplicationWrite("site-replication/remove", body)
}

// SiteReplicationEdit edits one peer in the group.
//
// Deprecated: use EditPeer, which takes SiteReplicationPeerEdit.
func (c *RustfsAdmin) SiteReplicationEdit(site SiteReplicationSite) error {
	return c.EditPeer(SiteReplicationPeerEdit{
		Name:          site.Name,
		Endpoint:      site.Endpoint,
		AccessKey:     site.AccessKey,
		SecretKey:     site.SecretKey,
		SkipTLSVerify: site.SkipTLSVerify,
		CACertPEM:     site.CACertPEM,
	})
}

// SiteReplicationRemove removes peers from the group.
//
// Deprecated: use RemovePeer.
func (c *RustfsAdmin) SiteReplicationRemove(names []string) error {
	return c.RemovePeer(names)
}

func (c *RustfsAdmin) SiteReplicationInfo() (SiteReplicationInfo, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "site-replication/info",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return SiteReplicationInfo{}, err
	}
	defer drainClose(resp)
	var info SiteReplicationInfo
	err = json.NewDecoder(resp.Body).Decode(&info)
	return info, err
}

// SiteReplicationResyncOp triggers, cancels, or inspects a resync against a
// peer site. operation is one of "start", "cancel", or "status". The server
// identifies the target peer by deploymentID.
func (c *RustfsAdmin) SiteReplicationResyncOp(operation string, peer SiteReplicationPeer) (SiteReplicationResync, error) {
	body, err := json.Marshal(peer)
	if err != nil {
		return SiteReplicationResync{}, err
	}
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "site-replication/resync/op",
		QueryValues: map[string][]string{
			"operation": {operation},
		},
		Content: body,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return SiteReplicationResync{}, err
	}
	defer drainClose(resp)
	var resync SiteReplicationResync
	err = json.NewDecoder(resp.Body).Decode(&resync)
	return resync, err
}

// Deliberately not surfaced by this client:
//
//   - site-replication/peer/edit, peer/remove and peer/edit-capabilities are
//     the peer-side half of a fan-out. The public routes above (edit, remove)
//     call them on every peer; calling them directly would change one site and
//     leave the rest diverged. edit-capabilities is the capability probe that
//     edit performs before changing endpoint targets or TLS settings.
//   - site-replication/devnull, netperf and rotate-svc-acct are diagnostics or
//     one-off recovery commands. They have no declarative end state, so they do
//     not belong in a Terraform resource.
//   - site-replication/peer/bucket-ops, peer/iam-item, peer/bucket-meta,
//     peer/join and state/edit are the internal peer replication protocol, and
//     peer/idp-settings is the IDP probe the add preflight makes. None of them
//     is an operator command.
//
// The routes below are the operator-facing remainder.

// Site-replication repair modes accepted by site-replication/repair. Dry-run
// returns a preflight plan and a token; execute requires that token.
const (
	SiteReplicationRepairDryRun  = "dry-run"
	SiteReplicationRepairExecute = "execute"
)

// SiteReplicationRepairRequest is the body of site-replication/repair.
type SiteReplicationRepairRequest struct {
	Mode           string `json:"mode"`
	PreflightToken string `json:"preflightToken,omitempty"`
	OperationID    string `json:"operationId,omitempty"`
}

// SiteReplicationRepairPreflight is the response of a dry-run. The token names
// the plan the server computed and is what an execute request must carry.
type SiteReplicationRepairPreflight struct {
	Mode           string                               `json:"mode"`
	Status         string                               `json:"status"`
	PreflightToken string                               `json:"preflightToken"`
	RetryEvents    int64                                `json:"retryEvents"`
	Sites          map[string]SiteReplicationRepairSite `json:"sites"`
}

// SiteReplicationRepairOperation is the response of an execute and of
// site-replication/repair/status. Execute runs synchronously and returns the
// terminal or partial operation; status re-reads a persisted operation.
type SiteReplicationRepairOperation struct {
	Mode        string                               `json:"mode"`
	OperationID string                               `json:"operationId"`
	Status      string                               `json:"status"`
	Sites       map[string]SiteReplicationRepairSite `json:"sites"`
	CreatedAt   string                               `json:"createdAt,omitempty"`
	UpdatedAt   string                               `json:"updatedAt,omitempty"`
	CompletedAt string                               `json:"completedAt,omitempty"`
}

// SiteReplicationRepairSite is one site's repair progress. Families groups the
// tasks by the subsystem they reconcile; RustFS currently reports the
// "replication" family.
type SiteReplicationRepairSite struct {
	DeploymentID string                                 `json:"deploymentId"`
	Name         string                                 `json:"name"`
	Families     map[string]SiteReplicationRepairFamily `json:"families"`
}

// SiteReplicationRepairFamily is the progress of one repair family on one site.
type SiteReplicationRepairFamily struct {
	Planned     int64                       `json:"planned"`
	Succeeded   int64                       `json:"succeeded"`
	Failed      int64                       `json:"failed"`
	RetryEvents int64                       `json:"retryEvents"`
	Tasks       []SiteReplicationRepairTask `json:"tasks"`
	Errors      []string                    `json:"errors"`
}

// SiteReplicationRepairTask is one reconciled item within a repair family.
type SiteReplicationRepairTask struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// SiteReplicationRepairPreflight asks the server to plan a repair without
// changing anything and returns the token an execute must present.
func (c *RustfsAdmin) SiteReplicationRepairPreflight() (SiteReplicationRepairPreflight, error) {
	body, err := json.Marshal(SiteReplicationRepairRequest{Mode: SiteReplicationRepairDryRun})
	if err != nil {
		return SiteReplicationRepairPreflight{}, err
	}
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "site-replication/repair",
		Content: body,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return SiteReplicationRepairPreflight{}, err
	}
	defer drainClose(resp)
	var preflight SiteReplicationRepairPreflight
	err = json.NewDecoder(resp.Body).Decode(&preflight)
	return preflight, err
}

// SiteReplicationRepairExecute runs a planned repair. It is synchronous: the
// returned operation is already terminal unless the server failed mid-flight.
// Re-sending the same operation id and token is safe; RustFS retires the token
// once the operation succeeds.
func (c *RustfsAdmin) SiteReplicationRepairExecute(preflightToken, operationID string) (SiteReplicationRepairOperation, error) {
	body, err := json.Marshal(SiteReplicationRepairRequest{
		Mode:           SiteReplicationRepairExecute,
		PreflightToken: preflightToken,
		OperationID:    operationID,
	})
	if err != nil {
		return SiteReplicationRepairOperation{}, err
	}
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "site-replication/repair",
		Content: body,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return SiteReplicationRepairOperation{}, err
	}
	defer drainClose(resp)
	var operation SiteReplicationRepairOperation
	err = json.NewDecoder(resp.Body).Decode(&operation)
	return operation, err
}

// SiteReplicationRepairStatus re-reads a persisted repair operation. RustFS
// keeps a bounded history and prunes the oldest successful operations, so a
// missing operation is not unusual for an old id; the error names it
// explicitly ("repair operation was not found") so callers can tell it apart
// from a genuine failure.
func (c *RustfsAdmin) SiteReplicationRepairStatus(operationID string) (SiteReplicationRepairOperation, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "site-replication/repair/status",
		QueryValues: map[string][]string{
			"operation-id": {operationID},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return SiteReplicationRepairOperation{}, err
	}
	defer drainClose(resp)
	var operation SiteReplicationRepairOperation
	err = json.NewDecoder(resp.Body).Decode(&operation)
	return operation, err
}

func (c *RustfsAdmin) siteReplicationWrite(relPath string, body []byte) error {
	reqData := RequestData{
		Method:  "PUT",
		RelPath: relPath,
		Content: body,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
