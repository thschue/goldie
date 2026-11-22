package contract_test

import (
	"net/http"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// A message that is empty once cleaned counts as no message: the client
// words the error from the status, not from an empty string before it.
func TestDecodeAPIErrorWithAMessageOfOnlyInvisibleRunesKeepsOnlyTheStatus(t *testing.T) {
	body := []byte(`{"error":{"code":"x","message":"\u200b\u202e\u001b","details":{"hint":"http://10.0.0.1/internal"}}}`)
	want := contract.APIError{Status: http.StatusNotFound}
	if got := contract.DecodeAPIError(http.StatusNotFound, body); got != want {
		t.Errorf("DecodeAPIError = %#v, want %#v", got, want)
	}
}
