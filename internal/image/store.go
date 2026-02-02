//go:build linux
// +build linux

package image

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"minidocker/pkg/fileutil"
)

// DefaultImagesDir is the default directory for image storage.
const DefaultImagesDir = "images"

// imageStore implements the Store interface.
type imageStore struct {
	root string // Root directory for image storage
}

// NewStore creates a new image store.
// If rootDir is empty, uses /var/lib/minidocker/images.
func NewStore(rootDir string) (Store, error) {
	if rootDir == "" {
		rootDir = filepath.Join("/var/lib/minidocker", DefaultImagesDir)
	}

	s := &imageStore{root: rootDir}

	// Ensure directory structure exists
	if err := s.init(); err != nil {
		return nil, fmt.Errorf("initialize image store: %w", err)
	}

	return s, nil
}

// init creates the initial directory structure and files.
func (s *imageStore) init() error {
	// Create blobs/sha256 directory
	blobsDir := filepath.Join(s.root, BlobsDir, "sha256")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		return fmt.Errorf("create blobs directory: %w", err)
	}

	// Create oci-layout file if not exists
	layoutPath := filepath.Join(s.root, ImageLayoutFile)
	if _, err := os.Stat(layoutPath); os.IsNotExist(err) {
		layout := ImageLayout{ImageLayoutVersion: ImageLayoutVersion}
		data, err := json.MarshalIndent(layout, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal oci-layout: %w", err)
		}
		if err := fileutil.AtomicWriteFile(layoutPath, data, 0644); err != nil {
			return fmt.Errorf("write oci-layout: %w", err)
		}
	}

	// Create index.json if not exists
	indexPath := filepath.Join(s.root, ImageIndexFile)
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		index := ocispec.Index{
			Versioned: ocispec.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: []ocispec.Descriptor{},
		}
		data, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal index.json: %w", err)
		}
		if err := fileutil.AtomicWriteFile(indexPath, data, 0644); err != nil {
			return fmt.Errorf("write index.json: %w", err)
		}
	}

	// Create repositories.json if not exists
	reposPath := filepath.Join(s.root, RepositoriesFile)
	if _, err := os.Stat(reposPath); os.IsNotExist(err) {
		repos := NewRepositories()
		data, err := json.MarshalIndent(repos, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal repositories.json: %w", err)
		}
		if err := fileutil.AtomicWriteFile(reposPath, data, 0644); err != nil {
			return fmt.Errorf("write repositories.json: %w", err)
		}
	}

	return nil
}

// Import imports an OCI tar archive and returns the image.
func (s *imageStore) Import(tarPath string, ref string) (*Image, error) {
	return importOCITar(s, tarPath, ref)
}

// List returns all images in the store.
func (s *imageStore) List() ([]*Image, error) {
	// Load index.json
	index, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	// Load repositories.json for tag lookup
	repos, err := s.loadRepositories()
	if err != nil {
		return nil, err
	}

	// Build reverse map: digest -> tags
	digestToTags := make(map[string][]string)
	for ref, dgst := range repos.Refs {
		digestToTags[dgst.String()] = append(digestToTags[dgst.String()], ref)
	}

	var images []*Image
	for _, desc := range index.Manifests {
		img, err := s.buildImage(desc.Digest, digestToTags[desc.Digest.String()])
		if err != nil {
			// Skip invalid manifests
			continue
		}
		images = append(images, img)
	}

	return images, nil
}

// Get retrieves an image by reference (name:tag or digest).
func (s *imageStore) Get(ref string) (*Image, error) {
	dgst, err := s.resolveReference(ref)
	if err != nil {
		return nil, err
	}

	// Load repositories for tags
	repos, err := s.loadRepositories()
	if err != nil {
		return nil, err
	}

	// Find tags for this digest
	var tags []string
	for r, d := range repos.Refs {
		if d == dgst {
			tags = append(tags, r)
		}
	}

	return s.buildImage(dgst, tags)
}

// Delete removes an image by reference.
func (s *imageStore) Delete(ref string) error {
	dgst, err := s.resolveReference(ref)
	if err != nil {
		return err
	}

	// Load repositories
	repos, err := s.loadRepositories()
	if err != nil {
		return err
	}

	// If ref is a tag, only remove the tag
	if !isDigestReference(ref) && !strings.Contains(ref, "@") {
		// Normalize tag ref (implies :latest when no tag is provided).
		// Keep backward compatibility with any previously stored "no-tag" refs.
		tagRef := normalizeTagRef(ref)
		if _, ok := repos.Refs[tagRef]; !ok && tagRef != ref {
			tagRef = ref
		}

		delete(repos.Refs, tagRef)

		// Check if other tags point to the same digest
		hasOtherRefs := false
		for _, d := range repos.Refs {
			if d == dgst {
				hasOtherRefs = true
				break
			}
		}

		// If other refs exist, just save repos and return
		if hasOtherRefs {
			return s.saveRepositories(repos)
		}
	} else {
		// If deleting by digest, remove all tags pointing to it
		for r, d := range repos.Refs {
			if d == dgst {
				delete(repos.Refs, r)
			}
		}
	}

	// No more references, delete the blobs
	manifest, err := s.GetManifest(dgst)
	if err != nil {
		return fmt.Errorf("get manifest for deletion: %w", err)
	}

	// Collect all blobs to delete (check if used by other manifests first)
	blobsToDelete := []digest.Digest{dgst, manifest.Config.Digest}
	for _, layer := range manifest.Layers {
		blobsToDelete = append(blobsToDelete, layer.Digest)
	}

	// Get all digests referenced by other manifests
	usedDigests := make(map[string]bool)
	index, err := s.loadIndex()
	if err != nil {
		return err
	}
	for _, desc := range index.Manifests {
		if desc.Digest == dgst {
			continue // Skip the manifest being deleted
		}
		usedDigests[desc.Digest.String()] = true
		otherManifest, err := s.GetManifest(desc.Digest)
		if err != nil {
			continue
		}
		usedDigests[otherManifest.Config.Digest.String()] = true
		for _, layer := range otherManifest.Layers {
			usedDigests[layer.Digest.String()] = true
		}
	}

	// Delete only unused blobs
	for _, blob := range blobsToDelete {
		if !usedDigests[blob.String()] {
			if err := s.deleteBlob(blob); err != nil {
				// Log but continue
				fmt.Fprintf(os.Stderr, "warning: failed to delete blob %s: %v\n", blob, err)
			}
		}
	}

	// Remove from index
	newManifests := make([]ocispec.Descriptor, 0, len(index.Manifests))
	for _, desc := range index.Manifests {
		if desc.Digest != dgst {
			newManifests = append(newManifests, desc)
		}
	}
	index.Manifests = newManifests
	if err := s.saveIndex(index); err != nil {
		return err
	}

	// Save updated repositories
	return s.saveRepositories(repos)
}

// Tag adds a tag to an existing image.
func (s *imageStore) Tag(source, target string) error {
	dgst, err := s.resolveReference(source)
	if err != nil {
		return err
	}

	repos, err := s.loadRepositories()
	if err != nil {
		return err
	}

	// Normalize target tag (implies :latest).
	if !isDigestReference(target) && !strings.Contains(target, "@") {
		target = normalizeTagRef(target)
	}

	repos.Refs[target] = dgst
	return s.saveRepositories(repos)
}

// GetBlob returns a reader for the blob content.
func (s *imageStore) GetBlob(dgst digest.Digest) (io.ReadCloser, error) {
	path := s.blobPath(dgst)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("blob not found: %s", dgst)
		}
		return nil, fmt.Errorf("open blob: %w", err)
	}
	return f, nil
}

// PutBlob writes content and returns its digest and size.
func (s *imageStore) PutBlob(r io.Reader) (digest.Digest, int64, error) {
	// Write to temp file while computing digest
	tmpFile, err := os.CreateTemp(s.root, "blob-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) // Clean up temp file
	}()

	// Use a digester to compute hash while writing
	digester := digest.SHA256.Digester()
	mw := io.MultiWriter(tmpFile, digester.Hash())

	size, err := io.Copy(mw, r)
	if err != nil {
		return "", 0, fmt.Errorf("write blob: %w", err)
	}
	tmpFile.Close()

	dgst := digester.Digest()

	// Move to final location
	blobPath := s.blobPath(dgst)
	if err := os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		return "", 0, fmt.Errorf("create blob directory: %w", err)
	}

	// If blob already exists, just return (deduplication)
	if _, err := os.Stat(blobPath); err == nil {
		return dgst, size, nil
	}

	if err := os.Rename(tmpPath, blobPath); err != nil {
		return "", 0, fmt.Errorf("move blob: %w", err)
	}

	return dgst, size, nil
}

// HasBlob checks if a blob exists.
func (s *imageStore) HasBlob(dgst digest.Digest) bool {
	path := s.blobPath(dgst)
	_, err := os.Stat(path)
	return err == nil
}

// PutBlobWithDigest writes content with expected digest verification.
// Returns error if the actual digest doesn't match expectedDigest.
func (s *imageStore) PutBlobWithDigest(r io.Reader, expectedDigest digest.Digest, expectedSize int64) error {
	// If blob already exists, skip writing (deduplication)
	if s.HasBlob(expectedDigest) {
		// Consume the reader to avoid connection issues
		_, _ = io.Copy(io.Discard, r)
		return nil
	}

	// Write to temp file while computing digest
	tmpFile, err := os.CreateTemp(s.root, "blob-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) // Clean up temp file on error
	}()

	// Use a digester to compute hash while writing
	digester := expectedDigest.Algorithm().Digester()
	mw := io.MultiWriter(tmpFile, digester.Hash())

	size, err := io.Copy(mw, r)
	if err != nil {
		return fmt.Errorf("write blob: %w", err)
	}
	tmpFile.Close()

	actualDigest := digester.Digest()

	// Verify digest
	if actualDigest != expectedDigest {
		return fmt.Errorf("digest mismatch: expected %s, got %s", expectedDigest, actualDigest)
	}

	// Verify size if provided (expectedSize > 0)
	if expectedSize > 0 && size != expectedSize {
		return fmt.Errorf("size mismatch: expected %d, got %d", expectedSize, size)
	}

	// Move to final location
	blobPath := s.blobPath(expectedDigest)
	if err := os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}

	// Double-check if blob was created by another process
	if s.HasBlob(expectedDigest) {
		return nil
	}

	if err := os.Rename(tmpPath, blobPath); err != nil {
		return fmt.Errorf("move blob: %w", err)
	}

	return nil
}

// AddManifest adds a manifest to the store and updates index.json.
// If ref is provided, it also updates repositories.json.
func (s *imageStore) AddManifest(manifestBytes []byte, manifestDigest digest.Digest, ref string) error {
	// Verify the manifest digest
	actualDigest := digest.FromBytes(manifestBytes)
	if actualDigest != manifestDigest {
		return fmt.Errorf("manifest digest mismatch: expected %s, got %s", manifestDigest, actualDigest)
	}

	// Write manifest as a blob
	blobPath := s.blobPath(manifestDigest)
	if err := os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}

	if err := fileutil.AtomicWriteFile(blobPath, manifestBytes, 0644); err != nil {
		return fmt.Errorf("write manifest blob: %w", err)
	}

	// Update index.json
	index, err := s.loadIndex()
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	// Check if manifest already exists in index
	manifestExists := false
	for _, desc := range index.Manifests {
		if desc.Digest == manifestDigest {
			manifestExists = true
			break
		}
	}

	if !manifestExists {
		// Add manifest to index
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    manifestDigest,
			Size:      int64(len(manifestBytes)),
		}
		index.Manifests = append(index.Manifests, desc)
		if err := s.saveIndex(index); err != nil {
			return fmt.Errorf("save index: %w", err)
		}
	}

	// Update repositories.json if ref is provided
	if ref != "" {
		repos, err := s.loadRepositories()
		if err != nil {
			return fmt.Errorf("load repositories: %w", err)
		}

		// Normalize tag reference
		if !isDigestReference(ref) && !strings.Contains(ref, "@") {
			ref = normalizeTagRef(ref)
		}

		repos.Refs[ref] = manifestDigest
		if err := s.saveRepositories(repos); err != nil {
			return fmt.Errorf("save repositories: %w", err)
		}
	}

	return nil
}

// Root returns the root directory of the image store.
func (s *imageStore) Root() string {
	return s.root
}

// GetManifest returns parsed manifest for an image.
func (s *imageStore) GetManifest(dgst digest.Digest) (*ocispec.Manifest, error) {
	r, err := s.GetBlob(dgst)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var manifest ocispec.Manifest
	if err := json.NewDecoder(r).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &manifest, nil
}

// GetConfig returns parsed config for an image.
func (s *imageStore) GetConfig(dgst digest.Digest) (*ocispec.Image, error) {
	r, err := s.GetBlob(dgst)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var config ocispec.Image
	if err := json.NewDecoder(r).Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return &config, nil
}

// Helper methods

func (s *imageStore) blobPath(dgst digest.Digest) string {
	return filepath.Join(s.root, BlobsDir, dgst.Algorithm().String(), dgst.Encoded())
}

func (s *imageStore) deleteBlob(dgst digest.Digest) error {
	return os.Remove(s.blobPath(dgst))
}

func (s *imageStore) loadIndex() (*ocispec.Index, error) {
	indexPath := filepath.Join(s.root, ImageIndexFile)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("read index.json: %w", err)
	}

	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse index.json: %w", err)
	}
	return &index, nil
}

func (s *imageStore) saveIndex(index *ocispec.Index) error {
	indexPath := filepath.Join(s.root, ImageIndexFile)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index.json: %w", err)
	}
	return fileutil.AtomicWriteFile(indexPath, data, 0644)
}

func (s *imageStore) loadRepositories() (*Repositories, error) {
	reposPath := filepath.Join(s.root, RepositoriesFile)
	data, err := os.ReadFile(reposPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewRepositories(), nil
		}
		return nil, fmt.Errorf("read repositories.json: %w", err)
	}

	var repos Repositories
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, fmt.Errorf("parse repositories.json: %w", err)
	}

	// Ensure map is initialized
	if repos.Refs == nil {
		repos.Refs = make(map[string]digest.Digest)
	}
	return &repos, nil
}

func (s *imageStore) saveRepositories(repos *Repositories) error {
	reposPath := filepath.Join(s.root, RepositoriesFile)
	data, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal repositories.json: %w", err)
	}
	return fileutil.AtomicWriteFile(reposPath, data, 0644)
}

// resolveReference resolves a reference to a digest.
// Reference can be:
// - digest format: sha256:abc123...
// - name:tag format: alpine:latest
// - name format: alpine (implies :latest)
func (s *imageStore) resolveReference(ref string) (digest.Digest, error) {
	// Digest reference forms:
	// - sha256:<hex> (pure digest)
	// - name@sha256:<hex> (named digest, common in registries)
	if strings.Contains(ref, "@") {
		dgst, err := parseNamedDigest(ref)
		if err != nil {
			return "", err
		}
		if !s.HasBlob(dgst) {
			return "", fmt.Errorf("image not found: %s", ref)
		}
		return dgst, nil
	}
	if isDigestReference(ref) {
		dgst, err := digest.Parse(ref)
		if err != nil {
			return "", fmt.Errorf("invalid digest: %w", err)
		}
		if !s.HasBlob(dgst) {
			return "", fmt.Errorf("image not found: %s", ref)
		}
		return dgst, nil
	}

	// Normalize tag reference (implies :latest when no tag is provided).
	normalizedRef := normalizeTagRef(ref)

	repos, err := s.loadRepositories()
	if err != nil {
		return "", err
	}

	dgst, ok := repos.Refs[normalizedRef]
	if !ok && normalizedRef != ref {
		// Backward compatibility: allow previously stored "no-tag" references.
		dgst, ok = repos.Refs[ref]
	}
	if !ok {
		// Docker Hub short-name compatibility (busybox -> library/busybox).
		for _, alias := range dockerHubRefAliases(normalizedRef) {
			if alias == normalizedRef || alias == ref {
				continue
			}
			if dgst, ok = repos.Refs[alias]; ok {
				return dgst, nil
			}
		}
		return "", fmt.Errorf("image not found: %s", normalizedRef)
	}
	return dgst, nil
}

// buildImage constructs an Image struct from manifest digest and tags.
func (s *imageStore) buildImage(dgst digest.Digest, tags []string) (*Image, error) {
	manifest, err := s.GetManifest(dgst)
	if err != nil {
		return nil, err
	}

	config, err := s.GetConfig(manifest.Config.Digest)
	if err != nil {
		return nil, err
	}

	// Calculate total size
	var size int64
	for _, layer := range manifest.Layers {
		size += layer.Size
	}

	// Get creation time
	var created time.Time
	if config.Created != nil {
		created = *config.Created
	}

	return &Image{
		ID:           dgst,
		RepoTags:     tags,
		Size:         size,
		Created:      created,
		Architecture: config.Architecture,
		OS:           config.OS,
		Manifest:     manifest,
		Config:       config,
	}, nil
}

// isDigestReference checks if the reference is a digest format.
func isDigestReference(ref string) bool {
	if ref == "" || strings.Contains(ref, "@") {
		return false
	}
	_, err := digest.Parse(ref)
	return err == nil
}

// normalizeTagRef normalizes a tag reference by adding ":latest" when no tag is present.
//
// This is similar to Docker's default tag behavior. It correctly handles references
// with registry ports, e.g. "localhost:5000/alpine" -> "localhost:5000/alpine:latest".
func normalizeTagRef(ref string) string {
	if ref == "" {
		return ""
	}
	if refHasTag(ref) {
		return ref
	}
	return ref + ":latest"
}

// refHasTag reports whether ref includes a tag.
// A tag is denoted by a ":" after the last "/" in the reference.
func refHasTag(ref string) bool {
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	return colon > slash
}

// splitRepoTag splits a reference into repository and tag.
// The tag is only recognized if it appears after the last "/".
func splitRepoTag(ref string) (repo, tag string) {
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, ""
}

// splitRegistry splits a repository into registry and remainder.
// If no registry is present, registry is empty and remainder is the input.
func splitRegistry(repo string) (registry, remainder string) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) == 1 {
		return "", repo
	}
	if isRegistryHost(parts[0]) {
		return parts[0], parts[1]
	}
	return "", repo
}

// isRegistryHost reports whether a name component should be treated as a registry.
// This follows Docker's heuristic: contains "." or ":" or is "localhost".
func isRegistryHost(component string) bool {
	return strings.Contains(component, ".") || strings.Contains(component, ":") || component == "localhost"
}

// dockerHubRefAliases returns alternative references for Docker Hub short-name compatibility.
// Examples:
// - busybox:latest -> library/busybox:latest
// - docker.io/library/busybox:latest -> library/busybox:latest, busybox:latest
func dockerHubRefAliases(ref string) []string {
	repo, tag := splitRepoTag(ref)
	if repo == "" || tag == "" {
		return nil
	}

	registry, remainder := splitRegistry(repo)
	if registry != "" && registry != "docker.io" && registry != "index.docker.io" {
		return nil
	}

	candidates := make(map[string]struct{})
	add := func(r string) {
		if r != "" {
			candidates[r] = struct{}{}
		}
	}

	base := repo
	if registry != "" {
		base = remainder
		add(base + ":" + tag)
	}

	if strings.HasPrefix(base, "library/") {
		add(strings.TrimPrefix(base, "library/") + ":" + tag)
	}

	if !strings.Contains(base, "/") {
		add("library/" + base + ":" + tag)
	}

	aliases := make([]string, 0, len(candidates))
	for r := range candidates {
		aliases = append(aliases, r)
	}
	return aliases
}

// parseNamedDigest parses "name@sha256:..." and returns the digest.
func parseNamedDigest(ref string) (digest.Digest, error) {
	parts := strings.SplitN(ref, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("invalid digest reference: %s", ref)
	}
	dgst, err := digest.Parse(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid digest reference: %w", err)
	}
	return dgst, nil
}
