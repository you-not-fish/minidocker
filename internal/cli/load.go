//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"minidocker/internal/image"
	"minidocker/internal/state"
)

var loadTag string

// loadCmd is the `minidocker load` command (similar to `docker load`).
// It imports an OCI tar archive into the local image store.
var loadCmd = &cobra.Command{
	Use:   "load [OPTIONS] -i FILE",
	Short: "从 OCI tar 归档导入镜像",
	Long: `从 OCI tar 归档导入镜像到本地存储。

支持的格式：
  - OCI Image Layout tar 归档（由 buildah, skopeo 等工具创建）

示例：
  minidocker load -i alpine.tar
  minidocker load -i alpine.tar -t alpine:latest`,
	RunE: runLoad,
}

var loadInput string

func init() {
	loadCmd.Flags().StringVarP(&loadInput, "input", "i", "", "要导入的 tar 归档文件路径（必需）")
	loadCmd.Flags().StringVarP(&loadTag, "tag", "t", "", "为导入的镜像添加标签（可选）")
	loadCmd.MarkFlagRequired("input")
}

func runLoad(cmd *cobra.Command, args []string) error {
	// Validate input file exists
	if _, err := os.Stat(loadInput); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", loadInput)
	}

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

	// Import the image
	fmt.Printf("Loading image from %s...\n", loadInput)
	img, err := store.Import(loadInput, loadTag)
	if err != nil {
		return fmt.Errorf("import image: %w", err)
	}

	// Print result
	id := img.ID.Encoded()
	if len(id) > 12 {
		id = id[:12]
	}

	fmt.Printf("Loaded image: %s\n", id)
	if len(img.RepoTags) > 0 {
		for _, tag := range img.RepoTags {
			fmt.Printf("  Tagged: %s\n", tag)
		}
	}

	return nil
}
