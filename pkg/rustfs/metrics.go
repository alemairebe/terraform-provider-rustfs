package rustfs

import (
	"context"
	"io"
)

// GetMetrics returns the raw metrics stream from GET /v3/metrics.
//
// RustFS registers the route but answers 501 NotImplemented on released servers
// (1.0.1 through 1.0.3); that answer is returned as an
// *UnsupportedFeatureError naming the route and the server version instead of
// the raw error payload.
func (c *RustfsAdmin) GetMetrics() (string, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "metrics",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return "", c.featureError(err, FeatureMetrics)
	}
	defer drainClose(resp)
	raw, err := io.ReadAll(resp.Body)
	return string(raw), err
}
