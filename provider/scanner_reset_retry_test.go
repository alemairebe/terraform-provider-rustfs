// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResetWithScannerLeaderRetryStopsWhenContextIsCanceled(t *testing.T) {
	restore := setScannerResetRetry(3, time.Hour)
	t.Cleanup(restore)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	_, err := resetWithScannerLeaderRetry(ctx, func() (string, error) {
		calls++
		return "", errors.New("scanner leader lock is busy")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
