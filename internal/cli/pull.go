//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/cobra"

	"minidocker/internal/distribution"
	"minidocker/internal/image"
	"minidocker/internal/state"
)

// pullCmd is the `minidocker pull` command.
var pullCmd = &cobra.Command{
	Use:   "pull [OPTIONS] IMAGE",
	Short: "从远端仓库拉取镜像",
	Long: `从远端仓库拉取镜像到本地存储。

支持的镜像引用格式：
  - alpine                    → docker.io/library/alpine:latest
  - alpine:3.18               → docker.io/library/alpine:3.18
  - nginx:latest              → docker.io/library/nginx:latest
  - gcr.io/project/image:tag  → gcr.io/project/image:tag
  - name@sha256:abc123...     → 按 digest 拉取

示例：
  minidocker pull alpine
  minidocker pull alpine:3.18
  minidocker pull gcr.io/distroless/static:latest
  minidocker pull nginx@sha256:abc123...`,
	Args: cobra.ExactArgs(1),
	RunE: runPull,
}

var (
	pullQuiet    bool
	pullPlatform string
)

func init() {
	pullCmd.Flags().BoolVarP(&pullQuiet, "quiet", "q", false, "静默模式，仅输出镜像 ID")
	pullCmd.Flags().StringVar(&pullPlatform, "platform", "linux/amd64", "目标平台 (os/arch)")
}

func runPull(cmd *cobra.Command, args []string) error {
	imageRef := args[0]

	// Determine root directory
	root := rootDir
	if root == "" {
		root = os.Getenv(state.RootDirEnvVar)
	}
	if root == "" {
		root = state.DefaultRootDir
	}

	// Create image store
	imageRoot := filepath.Join(root, image.DefaultImagesDir)
	store, err := image.NewStore(imageRoot)
	if err != nil {
		return fmt.Errorf("create image store: %w", err)
	}

	// Parse platform
	platform, err := parsePlatform(pullPlatform)
	if err != nil {
		return fmt.Errorf("invalid platform: %w", err)
	}

	// Configure pull options
	opts := &distribution.PullOptions{
		Quiet:    pullQuiet,
		Platform: platform,
		Output:   os.Stdout,
	}

	// Pull the image
	dgst, err := distribution.Pull(imageRef, store, opts)
	if err != nil {
		return fmt.Errorf("pull image: %w", err)
	}

	if pullQuiet {
		// In quiet mode, just print the digest
		fmt.Println(dgst.Encoded())
	}

	return nil
}

// parsePlatform parses a platform string like "linux/amd64" into a v1.Platform.
func parsePlatform(s string) (*v1.Platform, error) {
	var platform v1.Platform
	var variant string

	// Parse os/arch or os/arch/variant
	n, err := fmt.Sscanf(s, "%s/%s/%s", &platform.OS, &platform.Architecture, &variant)
	if err != nil || n < 2 {
		// Try simpler format
		n, err = fmt.Sscanf(s, "%s/%s", &platform.OS, &platform.Architecture)
		if err != nil || n != 2 {
			return nil, fmt.Errorf("expected format: os/arch[/variant], got: %s", s)
		}
	}
	if variant != "" {
		platform.Variant = variant
	}

	return &platform, nil
}
