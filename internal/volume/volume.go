//go:build linux
// +build linux

package volume

import (
	"regexp"
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
	// Type is the mount type (bind or volume)
	Type MountType `json:"type"`

	// Source is either:
	// - For bind: absolute host path
	// - For volume: volume name
	Source string `json:"source"`

	// Target is the absolute path inside the container
	Target string `json:"target"`

	// ReadOnly makes the mount read-only
	ReadOnly bool `json:"readOnly,omitempty"`

	// VolumePath is the resolved path for named volumes (internal use)
	// Populated after volume resolution
	VolumePath string `json:"volumePath,omitempty"`
}

// VolumeInfo contains metadata about a named volume
type VolumeInfo struct {
	// Name is the volume name
	Name string `json:"name"`

	// Path is the absolute path to volume data
	Path string `json:"path"`

	// CreatedAt is the creation timestamp
	CreatedAt time.Time `json:"createdAt"`

	// Driver is the volume driver (always "local" for now)
	Driver string `json:"driver"`

	// Labels for user metadata (reserved for future)
	Labels map[string]string `json:"labels,omitempty"`
}

// VolumeStore manages named volumes
type VolumeStore interface {
	// Create creates a new named volume
	// Returns error if volume already exists
	Create(name string) (*VolumeInfo, error)

	// Get retrieves a volume by name
	Get(name string) (*VolumeInfo, error)

	// List returns all volumes
	List() ([]*VolumeInfo, error)

	// Delete removes a volume
	// Returns error if volume doesn't exist
	Delete(name string) error

	// Exists checks if a volume exists
	Exists(name string) bool

	// GetPath returns the data path for a volume
	GetPath(name string) string
}

// DefaultVolumesDir is the default directory name for volumes
const DefaultVolumesDir = "volumes"

// volumeNameRegex matches valid volume names:
// - Must start with alphanumeric
// - Can contain alphanumeric, hyphen, underscore
// - Length 1-64 characters
var volumeNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// IsValidVolumeName checks if a volume name is valid
func IsValidVolumeName(name string) bool {
	if name == "" {
		return false
	}
	return volumeNameRegex.MatchString(name)
}
