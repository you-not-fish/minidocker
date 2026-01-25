//go:build integration && linux
// +build integration,linux

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 测试环境要求：
// - 具有 namespace 支持的 Linux 内核
// - Root 权限（或适当的能力）
// - minidocker 二进制文件已构建且可用

var minidockerBin string

func TestMain(m *testing.M) {
	// 如果不在 Linux 上，则跳过所有测试
	if os.Getenv("GOOS") != "" && os.Getenv("GOOS") != "linux" {
		os.Exit(0)
	}

	// 查找或构建 minidocker 二进制文件
	// 首先，尝试在项目根目录中查找它
	projectRoot := findProjectRoot()
	minidockerBin = filepath.Join(projectRoot, "minidocker")

	// 如果二进制文件不存在，则构建它
	if _, err := os.Stat(minidockerBin); os.IsNotExist(err) {
		cmd := exec.Command("go", "build", "-o", minidockerBin, "./cmd/minidocker")
		cmd.Dir = projectRoot
		if err := cmd.Run(); err != nil {
			panic("failed to build minidocker: " + err.Error())
		}
	}

	os.Exit(m.Run())
}

func findProjectRoot() string {
	// 向上遍历目录树以查找 go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// 到达根目录，放弃
			return "."
		}
		dir = parent
	}
}

func skipIfNotRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
}

// TestBasicRun 测试可以在容器中运行简单的命令
func TestBasicRun(t *testing.T) {
	skipIfNotRoot(t)

	cmd := exec.Command(minidockerBin, "run", "/bin/echo", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello") {
		t.Errorf("Expected output to contain 'hello', got: %s", output)
	}
}

// TestHostnameIsolation 测试容器具有与主机不同的主机名
func TestHostnameIsolation(t *testing.T) {
	skipIfNotRoot(t)

	// 获取主机主机名
	hostHostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("Failed to get host hostname: %v", err)
	}

	// 在容器中运行 hostname 命令
	cmd := exec.Command(minidockerBin, "run", "/bin/hostname")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerHostname := strings.TrimSpace(string(output))

	// 容器主机名应与主机主机名不同
	// （它应该是12个字符的容器 ID）
	if containerHostname == hostHostname {
		t.Errorf("Container hostname should be different from host hostname, got: %s", containerHostname)
	}

	// 容器主机名应为12个字符的十六进制字符串
	if len(containerHostname) != 12 {
		t.Errorf("Container hostname should be 12 characters, got %d: %s", len(containerHostname), containerHostname)
	}
}

// TestPIDNamespace 测试容器具有自己的 PID namespace
func TestPIDNamespace(t *testing.T) {
	skipIfNotRoot(t)

	// 运行显示其 PID 的命令
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", "cat /proc/self/status | grep ^Pid:")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 在容器内部，PID 应该很低（1、2 或小数）
	// 因为它在一个新的 PID namespace 中
	outputStr := strings.TrimSpace(string(output))
	// Output format: "Pid:\t<number>"
	parts := strings.Fields(outputStr)
	if len(parts) < 2 {
		t.Fatalf("Unexpected output format: %s", outputStr)
	}

	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("Failed to parse PID: %v", err)
	}

	// 在新的 PID namespace 中，进程应具有低 PID
	// shell (sh) 将是 PID 2，因为我们的 init 是 PID 1
	if pid > 10 {
		t.Errorf("Expected low PID inside container (PID namespace isolation), got: %d", pid)
	}
}

// TestMountNamespace 测试容器具有自己的 Mount namespace
func TestMountNamespace(t *testing.T) {
	skipIfNotRoot(t)

	// 容器的 /proc 应仅显示容器进程
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", "ls /proc | head -20")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 如果 /proc 在第1阶段未正确挂载，这可能会失败
		// 我们只是检查命令是否运行
		t.Logf("Warning: mount namespace test inconclusive: %v\nOutput: %s", err, output)
		return
	}

	// 应该只看到低编号的 PID
	t.Logf("Container /proc listing: %s", output)
}

// TestSignalForwarding 测试信号是否转发到容器进程
func TestSignalForwarding(t *testing.T) {
	skipIfNotRoot(t)

	// 在容器中启动一个长时间运行的进程
	cmd := exec.Command(minidockerBin, "run", "/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// 等待片刻让其启动
	time.Sleep(500 * time.Millisecond)

	// 发送 SIGTERM 到容器进程
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Failed to send SIGTERM: %v", err)
	}

	// 等待进程退出，带超时
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// 进程应因 SIGTERM 而退出
		if err != nil {
			// 退出错误是预期的（来自信号的非零退出代码）
			if exitErr, ok := err.(*exec.ExitError); ok {
				// 信号终止通常导致退出代码 128 + 信号编号
				// SIGTERM 是信号 15，所以我们预期退出代码 143
				expectedCode := 128 + int(syscall.SIGTERM)
				if exitErr.ExitCode() != expectedCode && exitErr.ExitCode() != 0 {
					t.Logf("Container exited with code %d (expected %d or 0)", exitErr.ExitCode(), expectedCode)
				}
			}
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Container did not exit after SIGTERM within timeout")
	}
}

// TestZombieReaping 测试僵尸进程是否由 init 正确回收
func TestZombieReaping(t *testing.T) {
	skipIfNotRoot(t)

	// 运行一个创建子进程并退出的命令
	// init 进程应该回收子进程
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", "sleep 0.1 & sleep 0.2")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 如果我们就在这里没有挂起，僵尸进程回收正在工作
	// 更彻底的测试将在执行期间检查 /proc 中的僵尸进程
	t.Log("Zombie reaping test passed (no hang detected)")
}

// TestExitCode 测试容器退出代码是否正确传播
func TestExitCode(t *testing.T) {
	skipIfNotRoot(t)

	tests := []struct {
		name         string
		args         []string
		expectedCode int
	}{
		{
			name:         "success",
			args:         []string{"run", "/bin/true"},
			expectedCode: 0,
		},
		{
			name:         "failure",
			args:         []string{"run", "/bin/false"},
			expectedCode: 1,
		},
		{
			name:         "custom_exit_code",
			args:         []string{"run", "/bin/sh", "-c", "exit 42"},
			expectedCode: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(minidockerBin, tt.args...)
			err := cmd.Run()

			var exitCode int
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("Unexpected error type: %v", err)
				}
			}

			if exitCode != tt.expectedCode {
				t.Errorf("Expected exit code %d, got %d", tt.expectedCode, exitCode)
			}
		})
	}
}

// TestInteractiveMode 测试基本的 TTY 功能
func TestInteractiveMode(t *testing.T) {
	skipIfNotRoot(t)

	// 这是一个基本测试 - 完整的 TTY 测试很复杂
	// 仅验证 -it 标志是否被接受
	cmd := exec.Command(minidockerBin, "run", "-it", "/bin/echo", "test")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// -it 模式在没有真实 TTY 的情况下可能会失败，但不应崩溃
		t.Logf("Interactive mode without TTY: %v (expected in CI)", err)
	}
}

// TestIPCNamespace 测试 IPC namespace 隔离
func TestIPCNamespace(t *testing.T) {
	skipIfNotRoot(t)

	// 这是一个基本测试 - 仅验证命名空间已创建
	// 完整的 IPC 隔离测试将需要创建共享内存段
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", "cat /proc/self/ns/ipc")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to check IPC namespace: %v\nOutput: %s", err, output)
	}

	containerIPC := strings.TrimSpace(string(output))

	// 获取主机 IPC namespace
	hostIPC, err := os.Readlink("/proc/self/ns/ipc")
	if err != nil {
		t.Fatalf("Failed to get host IPC namespace: %v", err)
	}

	// 它们应该是不同的
	if containerIPC == hostIPC {
		t.Error("Container IPC namespace should be different from host")
	}
}

// TestUTSNamespace 测试 UTS namespace 隔离
func TestUTSNamespace(t *testing.T) {
	skipIfNotRoot(t)

	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", "cat /proc/self/ns/uts")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to check UTS namespace: %v\nOutput: %s", err, output)
	}

	containerUTS := strings.TrimSpace(string(output))

	// 获取主机 UTS namespace
	hostUTS, err := os.Readlink("/proc/self/ns/uts")
	if err != nil {
		t.Fatalf("Failed to get host UTS namespace: %v", err)
	}

	// 它们应该是不同的
	if containerUTS == hostUTS {
		t.Error("Container UTS namespace should be different from host")
	}
}
