package pairings

import (
	"fmt"
	"net/http"
)

// ResponseError preserves the peer's response status for delivery decisions.
// It deliberately excludes response bodies, which may contain private data.
type ResponseError struct {
	Code int
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("paired network returned HTTP %d %s", e.Code, http.StatusText(e.Code))
}

func (e *ResponseError) StatusCode() int { return e.Code }
