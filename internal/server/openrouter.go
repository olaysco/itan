package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenRouter is the one provider whose catalogue changes weekly, and it is
// the reason someone would ever want to type a model id by hand. Serving the
// live list means picking "kimi via OpenRouter" is a click instead of a trip
// to the terminal to look up the exact slug.

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

var orCache struct {
	sync.Mutex
	at     time.Time
	models []orModel
}

// handleOpenRouterModels proxies OpenRouter's public catalogue. It needs no
// key (listing is open), is cached for an hour, and degrades to a clear
// message rather than an empty list the user cannot interpret.
func (s *Server) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	orCache.Lock()
	fresh := time.Since(orCache.at) < time.Hour && len(orCache.models) > 0
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
		httpErr(w, 502, "could not reach OpenRouter: "+err.Error()+" — you can still type a model id")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpErr(w, 502, fmt.Sprintf("OpenRouter returned HTTP %d — you can still type a model id", resp.StatusCode))
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
		httpErr(w, 502, "unreadable OpenRouter response")
		return
	}

	models := make([]orModel, 0, len(raw.Data))
	for _, m := range raw.Data {
		in, out := perMillion(m.Pricing.Prompt), perMillion(m.Pricing.Completion)
		vision := false
		for _, mod := range m.Architecture.InputModalities {
			if mod == "image" {
				vision = true
			}
		}
		models = append(models, orModel{
			ID: m.ID, Name: m.Name, Context: m.ContextLength,
			Price:  priceLabel(in, out),
			Vision: vision,
			Free:   in == 0 && out == 0,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

	orCache.Lock()
	orCache.at, orCache.models = time.Now(), models
	orCache.Unlock()
	writeJSON(w, map[string]any{"models": models})
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
