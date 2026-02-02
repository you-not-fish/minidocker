//go:build linux
// +build linux

package image

import (
	"bytes"
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestPutBlobWithDigestMismatch(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	data := []byte("hello")
	wrongDigest := digest.FromString("wrong")

	if err := store.PutBlobWithDigest(bytes.NewReader(data), wrongDigest, int64(len(data))); err == nil {
		t.Fatalf("expected digest mismatch error, got nil")
	}

	if store.HasBlob(wrongDigest) {
		t.Fatalf("unexpected blob present after digest mismatch")
	}
}

func TestPutBlobWithDigestSizeMismatch(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	data := []byte("hello")
	dgst := digest.FromBytes(data)

	if err := store.PutBlobWithDigest(bytes.NewReader(data), dgst, int64(len(data)+1)); err == nil {
		t.Fatalf("expected size mismatch error, got nil")
	}

	if store.HasBlob(dgst) {
		t.Fatalf("unexpected blob present after size mismatch")
	}
}
