package provider

import (
	"errors"
	"strings"
)

// ImageUnsupported reports whether a provider refused a request because the
// chosen model cannot accept images.
//
// This is worth singling out because the failure is late and expensive: a run
// composes for ten minutes, calls view_strip to check its own work, attaches
// the frames, and only then discovers the model is text-only. Recognizing it
// lets the run continue without the pictures instead of dying with everything
// already rendered.
//
// Hosts phrase it differently — OpenRouter answers 404 "No endpoints found
// that support image input", OpenAI-compatible servers tend to 400 with
// "does not support image", Ollama reports an unsupported modality — so the
// match is on a status that means "your request, not our server" plus wording
// about images. Retryable() already handles 5xx and 429, and those are never
// this.
func ImageUnsupported(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) {
		return false
	}
	switch he.Status {
	case 400, 404, 415, 422:
	default:
		return false
	}
	body := strings.ToLower(he.Body)
	mentionsImage := strings.Contains(body, "image") ||
		strings.Contains(body, "vision") ||
		strings.Contains(body, "multimodal") ||
		strings.Contains(body, "modality")
	if !mentionsImage {
		return false
	}
	// "image" alone is not enough — an invalid image payload is a different
	// problem from a model that cannot take one at all.
	return strings.Contains(body, "support") ||
		strings.Contains(body, "not accept") ||
		strings.Contains(body, "no endpoints") ||
		strings.Contains(body, "unsupported") ||
		strings.Contains(body, "text-only") ||
		strings.Contains(body, "text only")
}
