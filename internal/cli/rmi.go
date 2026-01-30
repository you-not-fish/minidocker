//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"minidocker/internal/image"
	"minidocker/internal/state"
)

var rmiForce bool

var rmiCmd = &cobra.Command{
	Use:   "rmi [OPTIONS] IMAGE [IMAGE...]",
	Short: "删除一个或多个镜像",
	Long:  `删除一个或多个本地镜像。如果镜像被多个标签引用，只删除指定的标签。`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRmi,
}

func init() {
	rmiCmd.Flags().BoolVarP(&rmiForce, "force", "f", false, "强制删除镜像")
}

func runRmi(cmd *cobra.Command, args []string) error {
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

	var lastErr error
	for _, ref := range args {
		// Get image to show what's being deleted
		img, err := store.Get(ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			lastErr = err
			continue
		}

		// Determine deletion behavior:
		// - Default: deleting by tag only removes that tag (if other tags exist).
		// - If --force is set: delete by digest (removes all tags + content).
		deleteRef := ref
		deletesImage := isDigestLikeRef(ref)
		if rmiForce {
			deleteRef = img.ID.String()
			deletesImage = true
		}

		// Delete the image
		if err := store.Delete(deleteRef); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to delete %s: %v\n", ref, err)
			lastErr = err
			continue
		}

		// Print results (Docker-like)
		if deletesImage {
			// When deleting by digest (or --force), all tags are removed.
			for _, tag := range img.RepoTags {
				fmt.Printf("Untagged: %s\n", tag)
			}
			fmt.Printf("Deleted: %s\n", shortImageID(img.ID))
			continue
		}

		// Tag-only deletion
		fmt.Printf("Untagged: %s\n", ref)
		// If this was the last tag, the underlying image is deleted too.
		if len(img.RepoTags) <= 1 {
			fmt.Printf("Deleted: %s\n", shortImageID(img.ID))
		}
	}

	return lastErr
}

func shortImageID(id interface{ Encoded() string }) string {
	encoded := id.Encoded()
	if len(encoded) > 12 {
		return encoded[:12]
	}
	return encoded
}

func isDigestLikeRef(ref string) bool {
	// Common digest forms:
	// - sha256:<hex>
	// - name@sha256:<hex>
	if strings.Contains(ref, "@") {
		return true
	}
	return strings.HasPrefix(ref, "sha256:") || strings.HasPrefix(ref, "sha384:") || strings.HasPrefix(ref, "sha512:")
}
