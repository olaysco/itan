package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryOn429ThenSuccess(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()

	var retries []RetryInfo
	r := WithRetry(NewOpenAI(srv.URL, "k", "test"), func(ri RetryInfo) { retries = append(retries, ri) })
	r.sleep = func(context.Context, time.Duration) error { return nil } // no real waiting in tests

	resp, err := r.Complete(context.Background(), Request{Model: "m", Messages: []Message{UserText("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "hi" || attempts != 3 || len(retries) != 2 {
		t.Fatalf("attempts=%d retries=%d text=%q", attempts, len(retries), resp.Text())
	}
}

func TestNoRetryOn400(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	r := WithRetry(NewOpenAI(srv.URL, "k", "test"), nil)
	r.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := r.Complete(context.Background(), Request{Model: "m", Messages: []Message{UserText("x")}})
	if err == nil || attempts != 1 {
		t.Fatalf("400 must not be retried: attempts=%d err=%v", attempts, err)
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 400 {
		t.Fatalf("expected typed HTTPError, got %T %v", err, err)
	}
}

func TestRetryHonorsRetryAfterHeader(t *testing.T) {
	he := &HTTPError{Status: 429, RetryAfter: 7 * time.Second}
	if d := backoff(1, he); d != 7*time.Second {
		t.Fatalf("Retry-After ignored: %s", d)
	}
	// Without a hint: exponential-ish with jitter, capped.
	if d := backoff(10, &HTTPError{Status: 500}); d > 45*time.Second {
		t.Fatalf("backoff uncapped: %s", d)
	}
}

func TestRetryableClassification(t *testing.T) {
	cases := map[int]bool{408: true, 429: true, 500: true, 529: true, 400: false, 401: false, 404: false}
	for status, want := range cases {
		if got := Retryable(&HTTPError{Status: status}); got != want {
			t.Errorf("Retryable(%d) = %v, want %v", status, got, want)
		}
	}
	if Retryable(context.Canceled) {
		t.Error("canceled context must not retry")
	}
	if !Retryable(errors.New("connection reset")) {
		t.Error("transport errors must retry")
	}
}
