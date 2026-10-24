package rooms

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/noopolis/moltnet/internal/pairings"
)

func TestDeliveryOutcomeClassifier(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 410, 422} {
		if !deliveryRejected(fmt.Errorf("wrapped: %w", &pairings.ResponseError{Code: code})) {
			t.Errorf("HTTP %d must reject", code)
		}
	}
	for _, code := range []int{408, 429, 500, 502, 503, 504} {
		if deliveryRejected(&pairings.ResponseError{Code: code}) {
			t.Errorf("HTTP %d must retry", code)
		}
	}
	for _, err := range []error{context.DeadlineExceeded, errors.New("network unavailable"), errors.New("relay is not connected")} {
		if deliveryRejected(err) {
			t.Errorf("%v must retry", err)
		}
	}
	if !deliveryRejected(fmt.Errorf("%w: local policy", errDeliveryRejected)) {
		t.Fatal("local policy must reject")
	}
}
