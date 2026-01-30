//go:build linux
// +build linux

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"minidocker/internal/image"
	"minidocker/internal/state"
)

var (
	imagesQuiet   bool
	imagesNoTrunc bool
	imagesFormat  string
)

var imagesCmd = &cobra.Command{
	Use:   "images [OPTIONS]",
	Short: "列出本地镜像",
	Long:  `列出本地存储的所有容器镜像。`,
	RunE:  runImages,
}

func init() {
	imagesCmd.Flags().BoolVarP(&imagesQuiet, "quiet", "q", false, "只显示镜像 ID")
	imagesCmd.Flags().BoolVar(&imagesNoTrunc, "no-trunc", false, "不截断输出")
	imagesCmd.Flags().StringVar(&imagesFormat, "format", "table", "输出格式 (table/json)")
}

func runImages(cmd *cobra.Command, args []string) error {
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

	// List images
	images, err := store.List()
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}

	// Handle quiet mode
	if imagesQuiet {
		for _, img := range images {
			id := img.ID.Encoded()
			if !imagesNoTrunc && len(id) > 12 {
				id = id[:12]
			}
			fmt.Println(id)
		}
		return nil
	}

	// Handle JSON format
	if imagesFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(images)
	}

	// Table format (default)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tTAG\tIMAGE ID\tCREATED\tSIZE")

	for _, img := range images {
		id := img.ID.Encoded()
		if !imagesNoTrunc && len(id) > 12 {
			id = id[:12]
		}

		created := formatRelativeTime(img.Created)
		size := formatSize(img.Size)

		if len(img.RepoTags) == 0 {
			// Image has no tags
			fmt.Fprintf(w, "<none>\t<none>\t%s\t%s\t%s\n", id, created, size)
		} else {
			// Output one row per tag
			for _, repoTag := range img.RepoTags {
				repo, tag := parseRepoTag(repoTag)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", repo, tag, id, created, size)
			}
		}
	}

	return w.Flush()
}

// parseRepoTag splits a reference into repository and tag.
func parseRepoTag(ref string) (repo, tag string) {
	// A tag separator ":" is only considered a tag if it appears after the last "/".
	// This correctly handles registry ports, e.g. "localhost:5000/alpine:latest".
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// formatRelativeTime formats a time as a human-readable relative time.
func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "N/A"
	}

	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "Less than a minute ago"
	case diff < time.Hour:
		minutes := int(diff.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case diff < 30*24*time.Hour:
		weeks := int(diff.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	case diff < 365*24*time.Hour:
		months := int(diff.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(diff.Hours() / 24 / 365)
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// formatSize formats a size in bytes as a human-readable string.
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case size < KB:
		return fmt.Sprintf("%dB", size)
	case size < MB:
		return fmt.Sprintf("%.2fKB", float64(size)/KB)
	case size < GB:
		return fmt.Sprintf("%.2fMB", float64(size)/MB)
	default:
		return fmt.Sprintf("%.2fGB", float64(size)/GB)
	}
}
