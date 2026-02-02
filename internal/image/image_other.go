//go:build !linux
// +build !linux

package image

import (
	"fmt"
	"io"
	"runtime"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var errNotSupported = fmt.Errorf("image operations are only supported on Linux (current: %s)", runtime.GOOS)

// Store stub for non-Linux platforms.
type stubStore struct{}

// NewStore returns an error on non-Linux platforms.
func NewStore(rootDir string) (Store, error) {
	return nil, errNotSupported
}

func (s *stubStore) Import(tarPath string, ref string) (*Image, error) {
	return nil, errNotSupported
}

func (s *stubStore) List() ([]*Image, error) {
	return nil, errNotSupported
}

func (s *stubStore) Get(ref string) (*Image, error) {
	return nil, errNotSupported
}

func (s *stubStore) Delete(ref string) error {
	return errNotSupported
}

func (s *stubStore) Tag(source, target string) error {
	return errNotSupported
}

func (s *stubStore) GetBlob(dgst digest.Digest) (io.ReadCloser, error) {
	return nil, errNotSupported
}

func (s *stubStore) PutBlob(r io.Reader) (digest.Digest, int64, error) {
	return "", 0, errNotSupported
}

func (s *stubStore) HasBlob(dgst digest.Digest) bool {
	return false
}

func (s *stubStore) GetManifest(dgst digest.Digest) (*ocispec.Manifest, error) {
	return nil, errNotSupported
}

func (s *stubStore) GetConfig(dgst digest.Digest) (*ocispec.Image, error) {
	return nil, errNotSupported
}

func (s *stubStore) PutBlobWithDigest(r io.Reader, expectedDigest digest.Digest, expectedSize int64) error {
	return errNotSupported
}

func (s *stubStore) AddManifest(manifestBytes []byte, manifestDigest digest.Digest, ref string) error {
	return errNotSupported
}

func (s *stubStore) Root() string {
	return ""
}
