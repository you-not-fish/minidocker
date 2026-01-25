//go:build linux
// +build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// 用于触发 init 模式的环境变量
const initEnvVar = "MINIDOCKER_INIT"

// 用于将容器配置传递给 init 进程的环境变量
const configEnvVar = "MINIDOCKER_CONFIG"

// Run 使用给定的配置创建并运行一个新容器。
//
// 注意：这个函数不应该调用 os.Exit。
// 退出码应由 CLI（或后续阶段的 daemon/manager）统一处理，这样才能自然支撑：
// - Phase 3: run -d / 状态持久化（state.json）
// - Phase 3+: 日志重定向、stop/exec
// - 可选：daemon / API 形态复用 runtime
func Run(config *ContainerConfig) (int, error) {
	// 创建将生成容器的父进程
	cmd, err := newParentProcess(config)
	if err != nil {
		return -1, fmt.Errorf("failed to create parent process: %w", err)
	}

	// 启动子进程（将成为容器 init）
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("failed to start container process: %w", err)
	}

	// 等待容器退出
	// 容器 init 的退出代码将被传播
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// 非 0 退出码是 `run` 的正常结果之一，不应在 runtime 层作为 Go error 传播。
			return exitErr.ExitCode(), nil
		}
		return -1, fmt.Errorf("container exited with error: %w", err)
	}

	return 0, nil
}

// newParentProcess 创建一个新命令，该命令将在启用命名空间隔离的情况下
// 重新执行当前二进制文件。
//
// 重新执行模式是必要的，因为：
// 1. Go 的运行时在 main() 运行之前会生成多个线程
// 2. 在 Go 中直接在当前进程内做会影响整个进程/线程组的 namespace 操作需要非常谨慎，
//    否则容易受到运行时多线程的影响而产生难以定位的问题
// 3. 通过 re-exec，子进程从一开始就处在目标 namespace 中，并进入明确的 init(PID1) 路径，
//    组织方式更贴近 runc
func newParentProcess(config *ContainerConfig) (*exec.Cmd, error) {
	// 重新执行当前二进制文件
	// /proc/self/exe 始终指向当前可执行文件
	cmd := exec.Command("/proc/self/exe")

	// 使用克隆标志配置命名空间隔离
	// 这些标志告诉内核为子进程创建新的命名空间
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | // 新的 UTS namespace (主机名)
			syscall.CLONE_NEWPID | // 新的 PID namespace (进程 ID)
			syscall.CLONE_NEWNS | // 新的 Mount namespace (文件系统挂载)
			syscall.CLONE_NEWIPC, // 新的 IPC namespace (System V IPC, POSIX 消息队列)
		// 注意：CLONE_NEWNET 未包含在第1阶段中。
		// 网络命名空间将在第7阶段 (feat-network-bridge) 中添加。
		// 目前，容器共享主机网络。

		// 注意：CLONE_NEWUSER 未包含在第1阶段中。
		// 用户命名空间将在第16阶段 (feat-rootless) 中添加。
	}

	// 序列化配置以传递给 init 进程
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize config: %w", err)
	}

	// 为 init 进程设置环境
	cmd.Env = append(os.Environ(),
		initEnvVar+"=1",
		configEnvVar+"="+string(configJSON),
	)

	// 连接标准输入输出
	if config.TTY {
		// 对于 TTY 模式，直接连接到终端
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		// 对于非 TTY 模式，仍然连接标准输入输出以进行基本 I/O
		// 第3阶段将为后台容器添加日志文件重定向
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	return cmd, nil
}

// GetContainerPID 返回容器 init 进程的 PID。
// 这供以后的阶段用于 exec、stop 等。
// 注意：这必须在 cmd.Start() 之后但在 cmd.Wait() 之前调用。
func GetContainerPID(cmd *exec.Cmd) int {
	if cmd.Process != nil {
		return cmd.Process.Pid
	}
	return 0
}

// setMountPropagation 将挂载传播设置为私有。
// 这可以防止容器内的挂载传播到主机。
func setMountPropagation() error {
	// 将所有挂载设为私有以防止传播到主机
	// 这相当于：mount --make-rprivate /
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("failed to set mount propagation to private: %w", err)
	}
	return nil
}
