// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccStorageInfo(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The data source reports live disk metrics (avail_space,
				// free_inodes, used_space) that move while other tests write
				// objects, so this test deliberately stays a single step and
				// never mirrors raw_json into an output: a second step would
				// compare those values against the previous read and fail
				// whenever they changed. ExpectNonEmptyPlan is not usable
				// either, because the refresh plan is empty when the metrics
				// happen to be stable.
				Config: testAccProviderConfig() + `
data "rustfs_storage_info" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "raw_json"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "backend.backend_type"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.#"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.0.path"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.0.total_space"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.0.state"),
					resource.TestCheckResourceAttrWith("data.rustfs_storage_info.test", "raw_json", func(v string) error {
						var parsed map[string]interface{}
						if err := json.Unmarshal([]byte(v), &parsed); err != nil {
							return err
						}
						if _, ok := parsed["info"]; !ok {
							return fmt.Errorf("raw_json missing info field")
						}
						return nil
					}),
				),
			},
		},
	})
}
