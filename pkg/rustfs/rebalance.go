// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
)

// StartRebalance starts a pool rebalance through
// POST /rustfs/admin/v3/rebalance/start, which answers with the rebalance
// trigger id. A deployment with a single pool has nothing to rebalance and
// answers 501 NotImplemented; the route itself is registered on RustFS 1.0.x.
func (c *RustfsAdmin) StartRebalance() error {
	reqData := RequestData{
		Method:  "POST",
		RelPath: "rebalance/start",
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
