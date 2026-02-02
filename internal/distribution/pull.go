//go:build linux
// +build linux

// Package distribution implements OCI distribution operations (pull/push).
package distribution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"minidocker/internal/image"
)

// PullOptions configures the pull operation.
type PullOptions struct {
	// Quiet suppresses progress output.
	Quiet bool
	// Platform specifies the target platform (default: linux/amd64).
	Platform *v1.Platform
	// Output is where progress messages are written (default: os.Stdout).
	Output io.Writer
}

// DefaultPullOptions returns the default pull options.
func DefaultPullOptions() *PullOptions {
	return &PullOptions{
		Quiet: false,
		Platform: &v1.Platform{
			OS:           "linux",
			Architecture: "amd64",
		},
		Output: nil, // Will use os.Stdout in Pull()
	}
}

// Pull downloads an image from a registry and stores it locally.
// Returns the manifest digest of the pulled image.
func Pull(ref string, store image.Store, opts *PullOptions) (digest.Digest, error) {
	if opts == nil {
		opts = DefaultPullOptions()
	}

	output := opts.Output
	if output == nil {
		output = os.Stdout
	}

	// Parse image reference
	imgRef, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", ref, err)
	}

	if !opts.Quiet {
		fmt.Fprintf(output, "Pulling %s...\n", imgRef.String())
	}

	// Configure remote options
	remoteOpts := []remote.Option{
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	}
	if opts.Platform != nil {
		remoteOpts = append(remoteOpts, remote.WithPlatform(*opts.Platform))
	}

	// Fetch the image
	img, err := remote.Image(imgRef, remoteOpts...)
	if err != nil {
		return "", fmt.Errorf("fetch image: %w", err)
	}

	// Get manifest
	manifest, err := img.Manifest()
	if err != nil {
		return "", fmt.Errorf("get manifest: %w", err)
	}

	// Convert manifest to OCI format and compute digest
	ociManifest := convertToOCIManifest(manifest)
	manifestBytes, err := json.Marshal(ociManifest)
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	ociDigest := digest.FromBytes(manifestBytes)

	// Check if manifest already exists
	if store.HasBlob(ociDigest) {
		if !opts.Quiet {
			fmt.Fprintf(output, "Image already exists: %s\n", ociDigest)
		}
		// Update index/repositories without re-downloading layers.
		refStr := formatReference(imgRef)
		if err := store.AddManifest(manifestBytes, ociDigest, refStr); err != nil {
			return "", fmt.Errorf("update manifest: %w", err)
		}
		return ociDigest, nil
	}

	// Download layers
	layers, err := img.Layers()
	if err != nil {
		return "", fmt.Errorf("get layers: %w", err)
	}

	if !opts.Quiet {
		fmt.Fprintf(output, "Downloading %d layer(s)...\n", len(layers))
	}

	for i, layer := range layers {
		layerDigest, err := layer.Digest()
		if err != nil {
			return "", fmt.Errorf("get layer %d digest: %w", i, err)
		}
		layerDgst := digest.Digest(layerDigest.String())

		// Skip if layer already exists
		if store.HasBlob(layerDgst) {
			if !opts.Quiet {
				fmt.Fprintf(output, "  Layer %d: %s (exists)\n", i+1, shortDigest(layerDgst))
			}
			continue
		}

		layerSize, err := layer.Size()
		if err != nil {
			return "", fmt.Errorf("get layer %d size: %w", i, err)
		}

		if !opts.Quiet {
			fmt.Fprintf(output, "  Layer %d: %s (%s)\n", i+1, shortDigest(layerDgst), formatSize(layerSize))
		}

		// Download layer (compressed)
		layerReader, err := layer.Compressed()
		if err != nil {
			return "", fmt.Errorf("download layer %d: %w", i, err)
		}

		if err := store.PutBlobWithDigest(layerReader, layerDgst, layerSize); err != nil {
			layerReader.Close()
			return "", fmt.Errorf("store layer %d: %w", i, err)
		}
		layerReader.Close()
	}

	// Download config
	configDigest := manifest.Config.Digest
	configDgst := digest.Digest(configDigest.String())

	if !store.HasBlob(configDgst) {
		if !opts.Quiet {
			fmt.Fprintf(output, "Downloading config: %s\n", shortDigest(configDgst))
		}

		configReader, err := img.RawConfigFile()
		if err != nil {
			return "", fmt.Errorf("get config: %w", err)
		}

		if err := store.PutBlobWithDigest(bytes.NewReader(configReader), configDgst, manifest.Config.Size); err != nil {
			return "", fmt.Errorf("store config: %w", err)
		}
	}

	// Store manifest
	refStr := formatReference(imgRef)
	if err := store.AddManifest(manifestBytes, ociDigest, refStr); err != nil {
		return "", fmt.Errorf("store manifest: %w", err)
	}

	if !opts.Quiet {
		fmt.Fprintf(output, "Pulled: %s\n", ociDigest)
	}

	return ociDigest, nil
}

// convertToOCIManifest converts a v1.Manifest to OCI format.
func convertToOCIManifest(m *v1.Manifest) *ocispec.Manifest {
	ociManifest := &ocispec.Manifest{
		Versioned: ocispec.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.Digest(m.Config.Digest.String()),
			Size:      m.Config.Size,
		},
		Layers: make([]ocispec.Descriptor, len(m.Layers)),
	}

	for i, layer := range m.Layers {
		mediaType := convertMediaType(string(layer.MediaType))
		ociManifest.Layers[i] = ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.Digest(layer.Digest.String()),
			Size:      layer.Size,
		}
	}

	return ociManifest
}

// convertMediaType converts Docker media types to OCI media types.
func convertMediaType(mediaType string) string {
	switch mediaType {
	case "application/vnd.docker.image.rootfs.diff.tar.gzip":
		return ocispec.MediaTypeImageLayerGzip
	case "application/vnd.docker.image.rootfs.diff.tar":
		return ocispec.MediaTypeImageLayer
	case "application/vnd.docker.container.image.v1+json":
		return ocispec.MediaTypeImageConfig
	default:
		// Keep original if already OCI or unknown
		return mediaType
	}
}

// formatReference formats a name.Reference to a tag string suitable for storage.
func formatReference(ref name.Reference) string {
	// Get repository name
	repo := ref.Context().RepositoryStr()
	registry := ref.Context().RegistryStr()

	// Build full reference
	fullRef := repo
	if registry != "index.docker.io" && registry != "" {
		fullRef = registry + "/" + repo
	}

	// Add tag if present
	if tag, ok := ref.(name.Tag); ok {
		return fullRef + ":" + tag.TagStr()
	}

	// Add digest if present
	if dgst, ok := ref.(name.Digest); ok {
		return fullRef + "@" + dgst.DigestStr()
	}

	return fullRef
}

// shortDigest returns a shortened digest for display.
func shortDigest(dgst digest.Digest) string {
	encoded := dgst.Encoded()
	if len(encoded) > 12 {
		return encoded[:12]
	}
	return encoded
}

// formatSize formats a byte size for human display.
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
