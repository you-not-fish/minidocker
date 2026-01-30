//go:build linux
// +build linux

package volume

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"minidocker/pkg/fileutil"
)

// volumeStore implements VolumeStore interface
type volumeStore struct {
	mu       sync.RWMutex
	rootDir  string // $MINIDOCKER_ROOT
	dataDir  string // $MINIDOCKER_ROOT/volumes
	metaPath string // $MINIDOCKER_ROOT/volumes/volumes.json
}

// volumeRegistry is the persisted state
type volumeRegistry struct {
	Volumes map[string]*VolumeInfo `json:"volumes"`
}

// NewVolumeStore creates a new volume store
func NewVolumeStore(rootDir string) (VolumeStore, error) {
	dataDir := filepath.Join(rootDir, DefaultVolumesDir)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create volumes directory: %w", err)
	}

	return &volumeStore{
		rootDir:  rootDir,
		dataDir:  dataDir,
		metaPath: filepath.Join(dataDir, "volumes.json"),
	}, nil
}

// Create creates a new named volume
func (s *volumeStore) Create(name string) (*VolumeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate name
	if !IsValidVolumeName(name) {
		return nil, fmt.Errorf("invalid volume name: %s (must be alphanumeric, can contain hyphen and underscore, 1-64 chars)", name)
	}

	registry, err := s.load()
	if err != nil {
		return nil, err
	}

	if _, exists := registry.Volumes[name]; exists {
		return nil, fmt.Errorf("volume %s already exists", name)
	}

	// Create volume data directory
	// Following Docker pattern: volumes/<name>/_data/
	volumePath := filepath.Join(s.dataDir, name, "_data")
	if err := os.MkdirAll(volumePath, 0755); err != nil {
		return nil, fmt.Errorf("create volume directory: %w", err)
	}

	info := &VolumeInfo{
		Name:      name,
		Path:      volumePath,
		CreatedAt: time.Now(),
		Driver:    "local",
	}

	registry.Volumes[name] = info
	if err := s.save(registry); err != nil {
		// Cleanup on save failure
		os.RemoveAll(filepath.Join(s.dataDir, name))
		return nil, err
	}

	return info, nil
}

// Get retrieves a volume by name
func (s *volumeStore) Get(name string) (*VolumeInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	registry, err := s.load()
	if err != nil {
		return nil, err
	}

	info, exists := registry.Volumes[name]
	if !exists {
		return nil, fmt.Errorf("volume %s not found", name)
	}

	return info, nil
}

// List returns all volumes
func (s *volumeStore) List() ([]*VolumeInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	registry, err := s.load()
	if err != nil {
		return nil, err
	}

	volumes := make([]*VolumeInfo, 0, len(registry.Volumes))
	for _, v := range registry.Volumes {
		volumes = append(volumes, v)
	}

	return volumes, nil
}

// Delete removes a volume
func (s *volumeStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	registry, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := registry.Volumes[name]; !exists {
		return fmt.Errorf("volume %s not found", name)
	}

	// Remove volume data directory
	volumeDir := filepath.Join(s.dataDir, name)
	if err := os.RemoveAll(volumeDir); err != nil {
		return fmt.Errorf("remove volume directory: %w", err)
	}

	delete(registry.Volumes, name)
	if err := s.save(registry); err != nil {
		return err
	}

	return nil
}

// Exists checks if a volume exists
func (s *volumeStore) Exists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	registry, err := s.load()
	if err != nil {
		return false
	}

	_, exists := registry.Volumes[name]
	return exists
}

// GetPath returns the data path for a volume
func (s *volumeStore) GetPath(name string) string {
	return filepath.Join(s.dataDir, name, "_data")
}

// load reads the volume registry from disk
func (s *volumeStore) load() (*volumeRegistry, error) {
	registry := &volumeRegistry{
		Volumes: make(map[string]*VolumeInfo),
	}

	data, err := os.ReadFile(s.metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, return empty registry
			return registry, nil
		}
		return nil, fmt.Errorf("read volumes.json: %w", err)
	}

	if err := json.Unmarshal(data, registry); err != nil {
		return nil, fmt.Errorf("parse volumes.json: %w", err)
	}

	// Ensure map is initialized
	if registry.Volumes == nil {
		registry.Volumes = make(map[string]*VolumeInfo)
	}

	return registry, nil
}

// save writes the volume registry to disk
func (s *volumeStore) save(registry *volumeRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal volumes.json: %w", err)
	}

	if err := fileutil.AtomicWriteFile(s.metaPath, data, 0644); err != nil {
		return fmt.Errorf("save volumes.json: %w", err)
	}

	return nil
}
