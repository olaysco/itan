package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/olaysco/itan/internal/config"
)

// OpenRouter lists hundreds of models and most of them cannot drive this
// toolset. The picker therefore shows config.OpenRouterSupported and nothing
// else; the live catalogue is consulted only to confirm those ids still exist
// and to attach current pricing and context. A model outside the set can be
// set by hand and is explicitly unsupported.

type orModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Context int    `json:"context"`
	Price   string `json:"price"`  // per-million in/out, human readable
	Vision  bool   `json:"vision"` // accepts image input
	Free    bool   `json:"free"`
}

// openRouterCatalogue is a variable so tests can point at a fixture instead
// of a third party.
var openRouterCatalogue = "https://openrouter.ai/api/v1/models"

// orCache memoises the enriched list. Freshness is the timestamp, never the
// length: curation can legitimately return nothing (every supported id
// withdrawn upstream), and treating empty as "not cached" would refetch on
// every open — hammering a third party precisely when it is unhappy.
var orCache struct {
	sync.Mutex
	at     time.Time
	models []orModel
}

// handleOpenRouterModels returns the supported set, enriched from OpenRouter's
// public catalogue. Listing needs no key, is cached for an hour, and when the
// catalogue is unreachable it falls back to the pinned set — an unreachable
// third party must not empty the picker.
func (s *Server) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	orCache.Lock()
	fresh := !orCache.at.IsZero() && time.Since(orCache.at) < time.Hour
	cached := orCache.models
	orCache.Unlock()
	if fresh {
		writeJSON(w, map[string]any{"models": cached, "cached": true})
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, openRouterCatalogue, nil)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, map[string]any{"models": pinnedSupported(), "supported": true, "pinned": true,
			"note": "could not reach OpenRouter (" + err.Error() + ") — showing the supported set without live pricing"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, map[string]any{"models": pinnedSupported(), "supported": true, "pinned": true,
			"note": fmt.Sprintf("OpenRouter returned HTTP %d — showing the supported set without live pricing", resp.StatusCode)})
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		httpErr(w, 502, err.Error())
		return
	}

	var raw struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Architecture  struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, map[string]any{"models": pinnedSupported(), "supported": true, "pinned": true,
			"note": "unreadable OpenRouter response — showing the supported set without live pricing"})
		return
	}

	// Index the catalogue, then walk the supported set — the curated order is
	// the picker's order, so the recommended model stays first instead of
	// wherever the alphabet puts it.
	live := make(map[string]orModel, len(raw.Data))
	for _, m := range raw.Data {
		in, out := perMillion(m.Pricing.Prompt), perMillion(m.Pricing.Completion)
		vision := false
		for _, mod := range m.Architecture.InputModalities {
			if mod == "image" {
				vision = true
			}
		}
		live[m.ID] = orModel{
			ID: m.ID, Name: m.Name, Context: m.ContextLength,
			Price:  priceLabel(in, out),
			Vision: vision,
			Free:   in == 0 && out == 0,
		}
	}

	models := make([]orModel, 0, len(config.OpenRouterSupported))
	var missing []string
	for _, sup := range config.OpenRouterSupported {
		m, ok := live[sup.ID]
		if !ok {
			// Supported here but absent upstream: renamed or withdrawn. Say so
			// rather than quietly shortening the list.
			missing = append(missing, sup.ID)
			continue
		}
		if !m.Vision {
			// A model that cannot see renders blind; it does not belong in the
			// supported set whatever the catalogue now says about it.
			missing = append(missing, sup.ID+" (no longer accepts images)")
			continue
		}
		m.Name = sup.Name // our label, not the vendor's marketing string
		models = append(models, m)
	}

	orCache.Lock()
	orCache.at, orCache.models = time.Now(), models
	orCache.Unlock()
	out := map[string]any{"models": models, "supported": true}
	if len(missing) > 0 {
		out["unavailable"] = missing
	}
	writeJSON(w, out)
}

// pinnedSupported is the answer when OpenRouter cannot be reached: the picker
// still offers exactly what itan supports, just without live pricing.
func pinnedSupported() []orModel {
	out := make([]orModel, 0, len(config.OpenRouterSupported))
	for _, s := range config.OpenRouterSupported {
		out = append(out, orModel{ID: s.ID, Name: s.Name, Vision: s.Vision})
	}
	return out
}

// resetORCache clears the memo; tests need each case to start cold.
func resetORCache() {
	orCache.Lock()
	orCache.at, orCache.models = time.Time{}, nil
	orCache.Unlock()
}

// perMillion converts OpenRouter's per-token price string to dollars per
// million tokens. An unparsable price reads as 0 rather than failing the
// whole listing over one odd entry.
func perMillion(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v * 1e6
}

func priceLabel(in, out float64) string {
	if in == 0 && out == 0 {
		return "free"
	}
	return fmt.Sprintf("$%s/$%s per M", trimPrice(in), trimPrice(out))
}

// trimPrice keeps money-shaped output: two decimals, except for models
// cheap enough that two decimals would round them to zero.
func trimPrice(v float64) string {
	if v >= 0.01 {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}
