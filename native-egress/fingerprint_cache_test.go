package main

import (
	"errors"
	"testing"
	"time"
)

func TestFPCacheHitAndExpiry(t *testing.T) {
	calls := 0
	c := NewFPCache(time.Minute, func(string) (string, error) {
		calls++
		return sampleDebug, nil
	})
	t0 := time.Unix(1000, 0)
	fp, ok := c.Get("acct", "/cfg", t0)
	if !ok || fp["x-app"] != "cli" {
		t.Fatalf("first get failed: %v %#v", ok, fp)
	}
	if _, ok := c.Get("acct", "/cfg", t0.Add(30*time.Second)); !ok || calls != 1 {
		t.Fatalf("should be cached, calls=%d", calls)
	}
	if _, ok := c.Get("acct", "/cfg", t0.Add(2*time.Minute)); !ok || calls != 2 {
		t.Fatalf("should recapture after TTL, calls=%d", calls)
	}
}

func TestFPCaptureFailureDegrades(t *testing.T) {
	c := NewFPCache(time.Minute, func(string) (string, error) { return "", errors.New("boom") })
	if _, ok := c.Get("a", "/c", time.Unix(1, 0)); ok {
		t.Fatal("capture failure must return ok=false")
	}
}
