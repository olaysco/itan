package provider

import (
	"errors"
	"fmt"
	"testing"
)

func TestImageUnsupportedRecognizesRealRefusals(t *testing.T) {
	yes := []*HTTPError{
		// The one that killed a ten-minute run.
		{Provider: "openrouter", Status: 404, Body: `{"error":{"message":"No endpoints found that support image input","code":404}}`},
		{Provider: "openai", Status: 400, Body: `{"error":{"message":"This model does not support image input."}}`},
		{Provider: "ollama", Status: 400, Body: `{"error":"unsupported modality: image"}`},
		{Provider: "x", Status: 422, Body: `model is text-only; images are not accepted`},
	}
	for _, e := range yes {
		if !ImageUnsupported(e) {
			t.Errorf("not recognized: %s", e.Error())
		}
	}

	no := []error{
		// A real server problem must keep its normal retry path.
		&HTTPError{Provider: "x", Status: 500, Body: "internal error processing image"},
		&HTTPError{Provider: "x", Status: 429, Body: "rate limited"},
		// A bad payload is a different bug and must not be silently degraded.
		&HTTPError{Provider: "x", Status: 400, Body: `{"error":"invalid base64 in image_url"}`},
		// Unrelated 404s must not be mistaken for it.
		&HTTPError{Provider: "x", Status: 404, Body: `{"error":"model not found"}`},
		errors.New("connection reset by peer"),
		fmt.Errorf("wrapped: %w", errors.New("dial tcp: timeout")),
	}
	for _, e := range no {
		if ImageUnsupported(e) {
			t.Errorf("false positive: %v", e)
		}
	}
}

// The check must see through wrapping, since callers add context.
func TestImageUnsupportedThroughWrapping(t *testing.T) {
	inner := &HTTPError{Provider: "openrouter", Status: 404, Body: "No endpoints found that support image input"}
	if !ImageUnsupported(fmt.Errorf("completing request: %w", inner)) {
		t.Error("wrapped error not recognized")
	}
}

// It must never claim a retryable failure is an image problem.
func TestImageUnsupportedAndRetryableAreDisjoint(t *testing.T) {
	for _, status := range []int{408, 429, 500, 502, 503} {
		e := &HTTPError{Status: status, Body: "no endpoints found that support image input"}
		if ImageUnsupported(e) {
			t.Errorf("status %d treated as an image refusal; it is retryable", status)
		}
		if !Retryable(e) {
			t.Errorf("status %d should be retryable", status)
		}
	}
}
