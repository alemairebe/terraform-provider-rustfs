// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

type AllClient struct {
	S3         *s3.Client
	RustClient rustfs.RustfsAdmin
}
