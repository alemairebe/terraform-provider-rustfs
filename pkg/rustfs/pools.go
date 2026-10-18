// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
)

type PoolInfo struct {
	Name string `json:"name"`
}

func (c *RustfsAdmin) ListPools() ([]PoolInfo, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "list-pools",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	var pools []PoolInfo
	err = json.NewDecoder(resp.Body).Decode(&pools)
	return pools, err
}
