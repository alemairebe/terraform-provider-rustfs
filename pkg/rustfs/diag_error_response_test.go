// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

// diagTestErrorXML renders the S3-shaped error body the admin client parses.
// It lives here so the batch-job and replication-diagnostic tests share one
// definition instead of one file owning a helper the other uses.
func diagTestErrorXML(code, message string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code + `</Code><Message>` + message + `</Message></Error>`
}
