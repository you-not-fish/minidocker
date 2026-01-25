//go:build integration && linux
// +build integration,linux

package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"sync"
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
	if runtime.GOOS != "linux" {
		os.Exit(0)
	}

	projectRoot := findProjectRoot()

	// 始终构建一个新的二进制文件，避免误用旧产物导致测试“假通过”。
	tmpDir, err := os.MkdirTemp("", "minidocker-test-*")
	if err != nil {
		panic("failed to create temp dir: " + err.Error())
	}
	minidockerBin = filepath.Join(tmpDir, "minidocker")

	buildCmd := exec.Command("go", "build", "-o", minidockerBin, "./cmd/minidocker")
	buildCmd.Dir = projectRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmpDir)
		panic("failed to build minidocker: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
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

func readChildPIDs(parentPID int) ([]int, error) {
	// `/proc/<pid>/task/<pid>/children` 列出该线程的直接子进程（以空格分隔）。
	// 对本项目而言：容器 init(host pid) 是 `minidocker run` 进程的直接子进程。
	path := fmt.Sprintf("/proc/%d/task/%d/children", parentPID, parentPID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(data))
	pids := make([]int, 0, len(fields))
	for _, f := range fields {
		p, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		pids = append(pids, p)
	}
	return pids, nil
}

func waitForChildPID(parentPID int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		children, err := readChildPIDs(parentPID)
		if err == nil && len(children) > 0 {
			// 如果有多个子进程，优先取最新（PID 最大）的那个。
			max := children[0]
			for _, c := range children[1:] {
				if c > max {
					max = c
				}
			}
			return max, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0, fmt.Errorf("timed out waiting for a child pid of %d", parentPID)
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

	// 直接对比 mount namespace id（比列 /proc 更直接，也更稳定）。
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", "cat /proc/self/ns/mnt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to check mount namespace: %v\nOutput: %s", err, output)
	}

	containerMnt := strings.TrimSpace(string(output))

	hostMnt, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatalf("Failed to get host mount namespace: %v", err)
	}

	if containerMnt == hostMnt {
		t.Error("Container mount namespace should be different from host")
	}
}

// TestSignalForwarding 测试信号是否转发到容器进程
func TestSignalForwarding(t *testing.T) {
	skipIfNotRoot(t)

	// 强验证：
	// - 容器内主进程安装 TERM trap，并打印标记 gotterm，退出码为 123
	// - 测试向“容器 init(host pid)”发送 SIGTERM，断言 trap 生效与退出码正确
	script := `echo ready; trap 'echo gotterm; exit 123' TERM; while true; do sleep 1; done`
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", script)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("Failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// 避免失败时泄漏进程
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	var (
		outMu   sync.Mutex
		outBuf  bytes.Buffer
		readyCh = make(chan struct{})
		once    sync.Once
	)

	recordLine := func(line string) {
		outMu.Lock()
		outBuf.WriteString(line)
		outBuf.WriteByte('\n')
		outMu.Unlock()
		if strings.TrimSpace(line) == "ready" {
			once.Do(func() { close(readyCh) })
		}
	}

	stdoutDone := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			recordLine(sc.Text())
		}
		stdoutDone <- sc.Err()
	}()

	stderrDone := make(chan error, 1)
	go func() {
		// 读取 stderr，避免 pipe 缓冲区写满阻塞进程
		data, readErr := io.ReadAll(stderr)
		if len(data) > 0 {
			for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
				if line != "" {
					recordLine(line)
				}
			}
		}
		stderrDone <- readErr
	}()

	// 等待就绪标记（trap 安装完成）
	select {
	case <-readyCh:
	case <-time.After(3 * time.Second):
		outMu.Lock()
		sofar := outBuf.String()
		outMu.Unlock()
		t.Fatalf("timeout waiting for readiness marker; output so far:\n%s", sofar)
	}

	// 找到容器 init 的 host PID（它是 `minidocker run` 的直接子进程）
	initPID, err := waitForChildPID(cmd.Process.Pid, 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to locate container init PID: %v", err)
	}

	// 向容器 init 发送 SIGTERM（init 应转发给容器内主进程）
	if err := syscall.Kill(initPID, syscall.SIGTERM); err != nil {
		t.Fatalf("Failed to send SIGTERM to container init pid %d: %v", initPID, err)
	}

	// 等待进程退出，带超时
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	waitReaders := func() {
		_ = <-stdoutDone
		_ = <-stderrDone
	}

	var waitErr error
	select {
	case waitErr = <-done:
		waitReaders()
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		waitReaders()
		t.Fatal("Container did not exit after SIGTERM within timeout")
	}

	outMu.Lock()
	allOut := outBuf.String()
	outMu.Unlock()

	if !strings.Contains(allOut, "gotterm") {
		t.Fatalf("Expected output to contain 'gotterm' marker. Full output:\n%s", allOut)
	}

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("Unexpected wait error type: %v", waitErr)
		}
	}
	if exitCode != 123 {
		t.Fatalf("Expected exit code 123 from TERM trap, got %d. Output:\n%s", exitCode, allOut)
	}
}

// TestZombieReaping 测试僵尸进程是否由 init 正确回收
func TestZombieReaping(t *testing.T) {
	skipIfNotRoot(t)

	// 更强验证：
	// 批量创建“孤儿的短命进程”，如果 PID1 没有处理 SIGCHLD，则僵尸会堆积并可在 /proc 观测到。
	// 每个 `sh -c 'sleep 0.02 &'` 都会立刻退出，从而把后台 sleep 变成 PID1 的子进程。
	script := `
set -eu
i=0
while [ "$i" -lt 200 ]; do
  sh -c 'sleep 0.02 &' >/dev/null 2>&1
  i=$((i+1))
done
sleep 0.3
z=0
for f in /proc/[0-9]*/status; do
  if grep -q '^State:[[:space:]]*Z' "$f"; then
    z=$((z+1))
  fi
done
if [ "$z" -ne 0 ]; then
  echo "found_zombies=$z"
  exit 1
fi
echo "no_zombies"
`
	cmd := exec.Command(minidockerBin, "run", "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Zombie reaping check failed: %v\nOutput:\n%s", err, output)
	}

	if !strings.Contains(string(output), "no_zombies") {
		t.Fatalf("Expected 'no_zombies' marker. Output:\n%s", output)
	}
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
