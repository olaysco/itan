package stock

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The video endpoint offers several renditions; the client must not blindly
// take the biggest (4K bytes we throw away at encode) nor the smallest.
func TestBestRenditionPrefersLargestUpTo1080p(t *testing.T) {
	type rend = struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Size   int64  `json:"size"`
	}
	vs := map[string]rend{
		"tiny":   {URL: "t", Width: 640, Height: 360},
		"small":  {URL: "s", Width: 960, Height: 540},
		"medium": {URL: "m", Width: 1280, Height: 720},
		"large":  {URL: "l", Width: 1920, Height: 1080},
		"uhd":    {URL: "u", Width: 3840, Height: 2160},
	}
	got, ok := bestRendition(vs, 0)
	if !ok || got.Width != 1920 {
		t.Fatalf("picked %dx%d, want 1920x1080", got.Width, got.Height)
	}

	// If nothing reaches 1080p, take the best available rather than nothing.
	delete(vs, "large")
	delete(vs, "uhd")
	if got, ok = bestRendition(vs, 0); !ok || got.Width != 1280 {
		t.Fatalf("fallback picked %dx%d, want 1280x720", got.Width, got.Height)
	}

	// min_width must be honored even when it excludes everything.
	if _, ok = bestRendition(vs, 4000); ok {
		t.Fatal("min_width was ignored")
	}
}

func TestSearchWithoutKeyIsActionable(t *testing.T) {
	_, err := New("").Search(context.Background(), Query{Text: "coastline"})
	if err == nil {
		t.Fatal("a missing key must be an error")
	}
	// The message has to say how to fix it, not just that it is broken.
	for _, want := range []string{"pixabay.com/api/docs", "media.pixabay_key", "PIXABAY_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func fakePixabay(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/videos/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "testkey" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"totalHits":2,"hits":[
		  {"id":11,"pageURL":"p11","tags":"coast, sunrise","duration":12,
		   "videos":{"medium":{"url":"%s/dl/11.mp4","width":1280,"height":720,"size":900},
		             "large":{"url":"%s/dl/11l.mp4","width":1920,"height":1080,"size":2400}}},
		  {"id":12,"pageURL":"p12","tags":"waves","duration":8,
		   "videos":{"large":{"url":"%s/dl/12.mp4","width":1920,"height":1080,"size":2000}}}]}`,
			"http://"+r.Host, "http://"+r.Host, "http://"+r.Host)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"totalHits":1,"hits":[
		  {"id":21,"pageURL":"p21","tags":"studio, portrait",
		   "largeImageURL":"%s/dl/21.jpg","imageWidth":1920,"imageHeight":1280}]}`, "http://"+r.Host)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("MEDIA-BYTES-" + filepath.Base(r.URL.Path)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("testkey")
	// Point the package endpoints at the fake for the duration of the test.
	oldP, oldV := photoEndpoint, videoEndpoint
	photoEndpoint, videoEndpoint = srv.URL+"/api/", srv.URL+"/api/videos/"
	t.Cleanup(func() { photoEndpoint, videoEndpoint = oldP, oldV })
	return c, srv
}

func TestSearchVideosAndDownload(t *testing.T) {
	c, _ := fakePixabay(t)
	hits, err := c.Search(context.Background(), Query{Text: "coastline sunrise", Kind: "video", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits", len(hits))
	}
	if hits[0].Width != 1920 {
		t.Errorf("did not take the 1080p rendition: %dx%d", hits[0].Width, hits[0].Height)
	}
	if !strings.Contains(hits[0].Describe(), "coast") {
		t.Errorf("description drops the tags: %s", hits[0].Describe())
	}

	dir := t.TempDir()
	path, err := c.Download(context.Background(), hits[0], dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "pixabay-11.mp4" {
		t.Errorf("name does not carry the source id: %s", filepath.Base(path))
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(body), "MEDIA-BYTES") {
		t.Fatalf("file not written: %v", err)
	}
	// A .part file left behind would later look like a valid cached download.
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(entries) > 0 {
		t.Errorf("temp file left behind: %v", entries)
	}

	// A second fetch must not re-download.
	before, _ := os.Stat(path)
	again, err := c.Download(context.Background(), hits[0], dir)
	if err != nil || again != path {
		t.Fatalf("re-download changed the path: %v %v", again, err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("cached file was rewritten")
	}
}

func TestSearchPhotos(t *testing.T) {
	c, _ := fakePixabay(t)
	hits, err := c.Search(context.Background(), Query{Text: "studio portrait", Kind: "photo", Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Kind != "photo" {
		t.Fatalf("got %+v", hits)
	}
	if !strings.Contains(hits[0].Describe(), "still") {
		t.Errorf("a photo should not be described as a clip: %s", hits[0].Describe())
	}
	path, err := c.Download(context.Background(), hits[0], t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".jpg" {
		t.Errorf("extension lost: %s", path)
	}
}

func TestSearchRejectsBadKey(t *testing.T) {
	_, srv := fakePixabay(t)
	_ = srv
	bad := New("wrong")
	_, err := bad.Search(context.Background(), Query{Text: "x", Kind: "video"})
	if err == nil || !strings.Contains(err.Error(), "media.pixabay_key") {
		t.Fatalf("a rejected key should name the setting to fix: %v", err)
	}
}

func TestEmptyResultsSuggestAWayForward(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/videos/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalHits":0,"hits":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	old := videoEndpoint
	videoEndpoint = srv.URL + "/api/videos/"
	defer func() { videoEndpoint = old }()

	_, err := New("k").Search(context.Background(), Query{Text: "nonexistent thing", Kind: "video"})
	if err == nil || !strings.Contains(err.Error(), "kind=photo") {
		t.Fatalf("an empty result should suggest what to try next: %v", err)
	}
}
