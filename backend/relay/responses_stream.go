package relay

import (
	"context"
	"net/http"
)

func ForwardResponsesStream(ctx context.Context, resp *http.Response, writer StreamResponseWriter) error {
	return forwardSSELines(ctx, resp, writer, true)
}
