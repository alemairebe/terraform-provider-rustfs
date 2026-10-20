// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
)

// RebalanceReport is the deployment-wide rebalance state reported by
// GET /rustfs/admin/v3/rebalance/status.
//
// The route answers 404 NoSuchResource when no rebalance has ever run, which
// callers distinguish from a route the server does not implement.
type RebalanceReport struct {
	ID              string                   `json:"id"`
	Pools           []RebalancePoolStatus    `json:"pools"`
	StoppedAt       *string                  `json:"stoppedAt"`
	StopPropagation RebalanceStopPropagation `json:"stopPropagation"`
}

// RebalancePoolStatus is one storage pool's part in a rebalance.
type RebalancePoolStatus struct {
	ID              int64                    `json:"id"`
	Status          string                   `json:"status"`
	Stopping        bool                     `json:"stopping"`
	Used            float64                  `json:"used"`
	LastError       *string                  `json:"lastError"`
	CleanupWarnings RebalanceCleanupWarnings `json:"cleanupWarnings"`
	// Progress is null when the pool is not moving data.
	Progress *RebalancePoolProgress `json:"progress"`
}

// RebalancePoolProgress is the live progress of one pool's move.
type RebalancePoolProgress struct {
	Objects          int64  `json:"objects"`
	Versions         int64  `json:"versions"`
	Bytes            int64  `json:"bytes"`
	RemainingBuckets int64  `json:"remainingBuckets"`
	Bucket           string `json:"bucket"`
	Object           string `json:"object"`
	Elapsed          int64  `json:"elapsed"`
	ETA              int64  `json:"eta"`
}

// RebalanceCleanupWarnings reports source-cleanup problems the rebalance hit.
type RebalanceCleanupWarnings struct {
	Count       int64                          `json:"count"`
	LastMessage *string                        `json:"lastMsg"`
	LastBucket  *string                        `json:"lastBucket"`
	LastObject  *string                        `json:"lastObject"`
	LastAt      *string                        `json:"lastAt"`
	Entries     []RebalanceCleanupWarningEntry `json:"entries"`
}

// RebalanceCleanupWarningEntry is one source-cleanup warning.
type RebalanceCleanupWarningEntry struct {
	Bucket    string  `json:"bucket"`
	Object    string  `json:"object"`
	Message   string  `json:"message"`
	Timestamp *string `json:"timestamp"`
}

// RebalanceStopPropagation reports how a stop request propagated across the
// deployment.
type RebalanceStopPropagation struct {
	LastAttemptAt             *string  `json:"lastAttemptAt"`
	FailedPeers               []string `json:"failedPeers"`
	TerminalReloadAttemptAt   *string  `json:"terminalReloadAttemptAt"`
	TerminalReloadFailedPeers []string `json:"terminalReloadFailedPeers"`
	PendingTerminalReload     bool     `json:"pendingTerminalReload"`
}

// StartRebalance starts a pool rebalance through
// POST /rustfs/admin/v3/rebalance/start, which answers with the rebalance
// trigger id. A deployment with a single pool has nothing to rebalance and
// answers 501 NotImplemented; the route itself is registered on RustFS 1.0.x.
func (c *RustfsAdmin) StartRebalance(ctx context.Context) (string, error) {
	reqData := RequestData{
		Method:  "POST",
		RelPath: "rebalance/start",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return "", err
	}
	defer drainClose(resp)

	var started struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		return "", err
	}
	return started.ID, nil
}

// RebalanceStatus returns the deployment's rebalance state from
// GET /rustfs/admin/v3/rebalance/status.
//
// RustFS answers 404 NoSuchResource while no rebalance has ever run. Servers
// that do not implement the route answer 404/405/501 without that code.
func (c *RustfsAdmin) RebalanceStatus(ctx context.Context) (RebalanceReport, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "rebalance/status",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return RebalanceReport{}, err
	}
	defer drainClose(resp)

	var report RebalanceReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return RebalanceReport{}, err
	}
	return report, nil
}

// StopRebalance stops the current rebalance through
// POST /rustfs/admin/v3/rebalance/stop. Stopping does not move already
// relocated data back. RustFS answers 404 NoSuchResource when there is no
// rebalance to stop, which is how a terminal run answers as well.
func (c *RustfsAdmin) StopRebalance(ctx context.Context) error {
	reqData := RequestData{
		Method:  "POST",
		RelPath: "rebalance/stop",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
