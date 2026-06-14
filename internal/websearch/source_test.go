package websearch

import (
	"context"
	"errors"
	"testing"
)

func TestStubSearchWeb(t *testing.T) {
	stub := &StubSource{WebResults: []WebResult{{Title: "A", URL: "https://example.com", Snippet: "s"}}}
	res, err := stub.SearchWeb(context.Background(), "test", 5)
	if err != nil || len(res) != 1 || res[0].Title != "A" {
		t.Fatalf("unexpected result: %v %v", res, err)
	}
}

func TestStubSearchImages(t *testing.T) {
	stub := &StubSource{ImageResults: []ImageResult{{URL: "https://img.example.com/1.jpg"}}}
	res, err := stub.SearchImages(context.Background(), "test", "test", 5)
	if err != nil || len(res) != 1 {
		t.Fatalf("unexpected result: %v %v", res, err)
	}
}

func TestStubError(t *testing.T) {
	stub := &StubSource{Err: errors.New("down")}
	_, err := stub.SearchWeb(context.Background(), "q", 3)
	if err == nil {
		t.Fatal("expected error")
	}
}
