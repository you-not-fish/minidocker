//go:build !linux
// +build !linux

package volume

import (
	"fmt"
	"runtime"
	"time"
)

// MountType represents the type of mount
type MountType string

const (
	// MountTypeBind is a bind mount from host to container
	MountTypeBind MountType = "bind"

	// MountTypeVolume is a named volume managed by minidocker
	MountTypeVolume MountType = "volume"
)

// Mount represents a single mount configuration
type Mount struct {
	Type       MountType `json:"type"`
	Source     string    `json:"source"`
	Target     string    `json:"target"`
	ReadOnly   bool      `json:"readOnly,omitempty"`
	VolumePath string    `json:"volumePath,omitempty"`
}

// VolumeInfo contains metadata about a named volume
type VolumeInfo struct {
	Name      string            `json:"name"`
	Path      string            `json:"path"`
	CreatedAt time.Time         `json:"createdAt"`
	Driver    string            `json:"driver"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// VolumeStore manages named volumes
type VolumeStore interface {
	Create(name string) (*VolumeInfo, error)
	Get(name string) (*VolumeInfo, error)
	List() ([]*VolumeInfo, error)
	Delete(name string) error
	Exists(name string) bool
	GetPath(name string) string
}

// DefaultVolumesDir is the default directory name for volumes
const DefaultVolumesDir = "volumes"

// IsValidVolumeName checks if a volume name is valid
func IsValidVolumeName(name string) bool {
	return false
}

// NewVolumeStore creates a new volume store
func NewVolumeStore(rootDir string) (VolumeStore, error) {
	return nil, fmt.Errorf("volume store is not supported on %s", runtime.GOOS)
}
