//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"minidocker/internal/cgroups"
	"minidocker/internal/image"
	"minidocker/internal/network"
	"minidocker/internal/snapshot"
	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var (
	rmForce   bool
	rmVolumes bool // Phase 10 预留：删除关联的卷
)

var rmCmd = &cobra.Command{
	Use:   "rm CONTAINER [CONTAINER...]",
	Short: "删除容器",
	Long: `删除一个或多个容器。

容器必须已停止，除非使用 -f 强制删除。
使用 -f 会先杀死运行中的容器再删除。

示例:
  minidocker rm my_container
  minidocker rm -f running_container
  minidocker rm container1 container2`,
	Args: cobra.MinimumNArgs(1),
	RunE: removeContainers,
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "强制删除运行中的容器")
	// Phase 10 预留：卷管理
	rmCmd.Flags().BoolVarP(&rmVolumes, "volumes", "v", false, "删除关联的卷（Phase 10 实现）")
}

func removeContainers(cmd *cobra.Command, args []string) error {
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	hasError := false
	for _, idOrPrefix := range args {
		if err := removeContainer(store, idOrPrefix); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing %s: %v\n", idOrPrefix, err)
			hasError = true
		} else {
			// 成功时输出容器 ID（与 Docker 行为一致）
			fmt.Println(idOrPrefix)
		}
	}

	if hasError {
		os.Exit(1)
	}
	return nil
}

func removeContainer(store *state.Store, idOrPrefix string) error {
	containerState, err := store.Get(idOrPrefix)
	if err != nil {
		// 幂等：删除不存在的容器应成功（tests/integration/state_test.go: TestRmIdempotent）
		// 但“短 ID 太短 / 前缀歧义 / 状态损坏”等属于真实错误，应返回给用户。
		if strings.Contains(err.Error(), "container not found") {
			return nil
		}
		return err
	}

	// 检查容器是否正在运行
	if containerState.IsRunning() {
		if !rmForce {
			return fmt.Errorf("container %s is running, use -f to force remove", idOrPrefix)
		}

		// 强制删除：先 kill
		pid := containerState.Pid
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			if err != syscall.ESRCH {
				// 不是"进程不存在"错误
				return fmt.Errorf("failed to kill container: %w", err)
			}
			// 进程已不存在，继续删除
		} else {
			// 等待进程退出
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(pid, 0); err != nil {
					if err == syscall.ESRCH {
						break
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	// Phase 9: 清理快照（unmount overlay + 删除 upper/work）
	// 清理顺序: Snapshot → Network → cgroup → 状态目录
	// Note: snapshot cleanup is idempotent. We attempt cleanup even if state.json
	// is missing SnapshotPath (e.g., older state or partial failures), to avoid
	// leaking overlay mounts and upper/work directories.
	imageRoot := filepath.Join(store.RootDir, image.DefaultImagesDir)
	if imageStore, err := image.NewStore(imageRoot); err == nil {
		if snapshotter, err := snapshot.NewSnapshotter(store.RootDir, imageStore); err == nil {
			// 忽略清理错误（快照可能已被清理）
			_ = snapshotter.Remove(containerState.ID)
		}
	}

	// Phase 7: 清理网络（仅 bridge 模式会创建宿主侧资源：veth/iptables/IPAM）
	// 清理顺序: Snapshot → Network → cgroup → 状态目录
	if containerState.NetworkState != nil && containerState.NetworkState.Mode == string(network.NetworkModeBridge) {
		if manager, err := network.NewManager(store.RootDir); err == nil {
			// 转换 state.NetworkState 到 network.NetworkState
			netState := &network.NetworkState{
				Mode:          network.NetworkMode(containerState.NetworkState.Mode),
				IPAddress:     containerState.NetworkState.IPAddress,
				Gateway:       containerState.NetworkState.Gateway,
				MacAddress:    containerState.NetworkState.MacAddress,
				VethHost:      containerState.NetworkState.VethHost,
				VethContainer: containerState.NetworkState.VethContainer,
			}
			if len(containerState.NetworkState.PortMappings) > 0 {
				netState.PortMappings = make([]network.PortMapping, len(containerState.NetworkState.PortMappings))
				for i, pm := range containerState.NetworkState.PortMappings {
					netState.PortMappings[i] = network.PortMapping{
						HostIP:        pm.HostIP,
						HostPort:      pm.HostPort,
						ContainerPort: pm.ContainerPort,
						Protocol:      pm.Protocol,
					}
				}
			}
			// 忽略清理错误（网络资源可能已被清理）
			_ = manager.Teardown(containerState.ID, netState)
		}
	}

	// Phase 6: 清理 cgroup（如果存在）
	// 需要在删除状态目录前清理，因为状态目录中存储了 cgroup 路径
	if containerState.CgroupPath != "" {
		if manager, err := cgroups.NewManager(); err == nil {
			// 忽略清理错误（cgroup 可能已被清理）
			_ = manager.Destroy(containerState.CgroupPath)
		}
	}

	// 删除容器状态目录
	if err := store.Delete(containerState.ID); err != nil {
		return fmt.Errorf("failed to delete container: %w", err)
	}

	return nil
}
